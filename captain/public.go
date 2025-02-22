package captain

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/safing/portbase/database"
	"github.com/safing/portbase/log"
	"github.com/safing/portbase/metrics"
	"github.com/safing/portbase/modules"
	"github.com/safing/spn/cabin"
	"github.com/safing/spn/conf"
	"github.com/safing/spn/docks"
	"github.com/safing/spn/hub"
	"github.com/safing/spn/navigator"
	"github.com/safing/spn/patrol"
)

const (
	maintainStatusInterval    = 15 * time.Minute
	maintainStatusUpdateDelay = 5 * time.Second
)

var (
	publicIdentity    *cabin.Identity
	publicIdentityKey = "core:spn/public/identity"

	publicIdentityUpdateTask *modules.Task
	statusUpdateTask         *modules.Task
)

func loadPublicIdentity() (err error) {
	var changed bool

	publicIdentity, changed, err = cabin.LoadIdentity(publicIdentityKey)
	switch {
	case err == nil:
		// load was successful
		log.Infof("spn/captain: loaded public hub identity %s", publicIdentity.Hub.ID)
	case errors.Is(err, database.ErrNotFound):
		// does not exist, create new
		publicIdentity, err = cabin.CreateIdentity(module.Ctx, conf.MainMapName)
		if err != nil {
			return fmt.Errorf("failed to create new identity: %w", err)
		}
		publicIdentity.SetKey(publicIdentityKey)
		changed = true

		log.Infof("spn/captain: created new public hub identity %s", publicIdentity.ID)
	default:
		// loading error, abort
		return fmt.Errorf("failed to load public identity: %w", err)
	}

	// Save to database if the identity changed.
	if changed {
		err = publicIdentity.Save()
		if err != nil {
			return fmt.Errorf("failed to save new/updated identity to database: %w", err)
		}
	}

	// Set available networks.
	conf.SetHubNetworks(
		publicIdentity.Hub.Info.IPv4 != nil,
		publicIdentity.Hub.Info.IPv6 != nil,
	)

	// Set Home Hub before updating the hub on the map, as this would trigger a
	// recalculation without a Home Hub.
	ok := navigator.Main.SetHome(publicIdentity.ID, nil)
	// Always update the navigator in any case in order to sync the reference to
	// the active struct of the identity.
	navigator.Main.UpdateHub(publicIdentity.Hub)
	// Setting the Home Hub will have failed if the identidy was only just
	// created - try again if it failed.
	if !ok {
		ok = navigator.Main.SetHome(publicIdentity.ID, nil)
		if !ok {
			return errors.New("failed to set self as home hub")
		}
	}

	return nil
}

func prepPublicIdentityMgmt() error {
	publicIdentityUpdateTask = module.NewTask(
		"maintain public identity",
		maintainPublicIdentity,
	)

	statusUpdateTask = module.NewTask(
		"maintain public status",
		maintainPublicStatus,
	).Repeat(maintainStatusInterval)

	return module.RegisterEventHook(
		"config",
		"config change",
		"update public identity from config",
		func(_ context.Context, _ interface{}) error {
			// trigger update in 5 minutes
			publicIdentityUpdateTask.Schedule(time.Now().Add(5 * time.Minute))
			return nil
		},
	)
}

// TriggerHubStatusMaintenance queues the Hub status update task to be executed.
func TriggerHubStatusMaintenance() {
	if statusUpdateTask != nil {
		statusUpdateTask.Queue()
	}
}

func maintainPublicIdentity(ctx context.Context, task *modules.Task) error {
	changed, err := publicIdentity.MaintainAnnouncement(false)
	if err != nil {
		return fmt.Errorf("failed to maintain announcement: %w", err)
	}

	if !changed {
		return nil
	}

	// Update on map.
	navigator.Main.UpdateHub(publicIdentity.Hub)
	log.Debug("spn/captain: updated own hub on map after announcement change")

	// export announcement
	announcementData, err := publicIdentity.ExportAnnouncement()
	if err != nil {
		return fmt.Errorf("failed to export announcement: %w", err)
	}

	// forward to other connected Hubs
	gossipRelayMsg("", GossipHubAnnouncementMsg, announcementData)

	// manage docks in order to react to possibly changed transports
	if managePiersTask != nil {
		managePiersTask.Queue()
	}

	return nil
}

func maintainPublicStatus(ctx context.Context, task *modules.Task) error {
	// Get current lanes.
	cranes := docks.GetAllAssignedCranes()
	lanes := make([]*hub.Lane, 0, len(cranes))
	for _, crane := range cranes {
		// Ignore private, stopped or stopping cranes.
		if !crane.Public() || crane.Stopped() || crane.IsStopping() {
			continue
		}

		// Get measurements.
		measurements := crane.ConnectedHub.GetMeasurements()
		latency, _ := measurements.GetLatency()
		capacity, _ := measurements.GetCapacity()

		// Add crane lane.
		lanes = append(lanes, &hub.Lane{
			ID:       crane.ConnectedHub.ID,
			Latency:  latency,
			Capacity: capacity,
		})
	}
	// Sort Lanes for comparing.
	hub.SortLanes(lanes)

	// Get system load and convert to fixed steps.
	var load int
	loadAvg, ok := metrics.LoadAvg15()
	switch {
	case !ok:
		load = -1
	case loadAvg >= 1:
		load = 100
	case loadAvg >= 0.95:
		load = 95
	case loadAvg >= 0.8:
		load = 80
	default:
		load = 0
	}
	if loadAvg >= 0.8 {
		log.Warningf("spn/captain: publishing 15m system load average of %.2f as %d", loadAvg, load)
	}

	// Set flags.
	var flags []string
	if !patrol.HTTPSConnectivityConfirmed() {
		flags = append(flags, hub.FlagNetError)
	}
	// Sort Lanes for comparing.
	sort.Strings(flags)

	// Run maintenance with the new data.
	changed, err := publicIdentity.MaintainStatus(lanes, &load, flags, false)
	if err != nil {
		return fmt.Errorf("failed to maintain status: %w", err)
	}

	if !changed {
		return nil
	}

	// Update on map.
	navigator.Main.UpdateHub(publicIdentity.Hub)
	log.Debug("spn/captain: updated own hub on map after status change")

	// export status
	statusData, err := publicIdentity.ExportStatus()
	if err != nil {
		return fmt.Errorf("failed to export status: %w", err)
	}

	// forward to other connected Hubs
	gossipRelayMsg("", GossipHubStatusMsg, statusData)

	log.Infof(
		"spn/captain: updated status with load %d and current lanes: %v",
		publicIdentity.Hub.Status.Load,
		publicIdentity.Hub.Status.Lanes,
	)
	return nil
}

func publishShutdownStatus() {
	// Create offline status.
	offlineStatusData, err := publicIdentity.MakeOfflineStatus()
	if err != nil {
		log.Errorf("spn/captain: failed to create offline status: %s", err)
		return
	}

	// Forward to other connected Hubs.
	gossipRelayMsg("", GossipHubStatusMsg, offlineStatusData)

	// Leave some time for the message to broadcast.
	time.Sleep(2 * time.Second)

	log.Infof("spn/captain: broadcasted offline status")
}
