package docks

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/safing/portbase/log"
	"github.com/safing/spn/conf"
	"github.com/safing/spn/hub"
	"github.com/safing/spn/ships"
	"github.com/safing/spn/terminal"
)

var hubImportLock sync.Mutex

func ImportAndVerifyHubInfo(ctx context.Context, hubID string, announcementData, statusData []byte, scope hub.Scope) (h *hub.Hub, forward bool, tErr *terminal.Error) {
	// Synchronize import, as we might easily learn of a new hub from different
	// gossip channels simultaneously.
	hubImportLock.Lock()
	defer hubImportLock.Unlock()

	// Check arguments.
	if announcementData == nil && statusData == nil {
		return nil, false, terminal.ErrInternalError.With("no announcement or status supplied")
	}

	// Import Announcement, if given.
	var err error
	var forwardPart bool
	if announcementData != nil {
		h, forwardPart, err = hub.ApplyAnnouncement(nil, announcementData, scope, false)
		if err != nil {
			return h, false, terminal.ErrInternalError.With("failed to apply announcement: %w", err)
		}
		if forwardPart {
			forward = true
		}
	}

	// Import Status, if given.
	if statusData != nil {
		h, forwardPart, err = hub.ApplyStatus(h, statusData, scope, false)
		if err != nil {
			return h, false, terminal.ErrInternalError.With("failed to apply status: %w", err)
		}
		if forwardPart {
			forward = true
		}
	}

	// Check if the given hub ID matches.
	if hubID != "" && h.ID != hubID {
		return nil, false, terminal.ErrInternalError.With("hub mismatch")
	}

	// Verify hub if not yet verified.
	if !h.Verified() && conf.PublicHub() {
		if h.Info.IPv4 != nil {
			err = verifyHubIP(ctx, h, h.Info.IPv4)
			if err != nil {
				return nil, forward, terminal.ErrIntegrity.With("failed to verify IPv4 address %s: %w", h.Info.IPv4, err)
			}
		}
		if h.Info.IPv6 != nil {
			err = verifyHubIP(ctx, h, h.Info.IPv6)
			if err != nil {
				return nil, forward, terminal.ErrIntegrity.With("failed to verify IPv6 address %s: %w", h.Info.IPv6, err)
			}
		}
		h.Lock()
		h.VerifiedIPs = true
		h.Unlock()
		log.Infof("spn/docks: verified IPs of %s: IPv4=%s IPv6=%s", h, h.Info.IPv4, h.Info.IPv6)
	}

	// Save the Hub to the database.
	err = h.Save()
	if err != nil {
		return nil, forward, terminal.ErrInternalError.With("failed to persist hub: %w", err)
	}

	// Save the raw messages to the database.
	if announcementData != nil {
		err = hub.SaveRawHubMsg(h.ID, h.Scope, "announcement", announcementData)
		if err != nil {
			log.Warningf("spn/docks: failed to save raw announcement msg: %w", err)
		}
	}
	if statusData != nil {
		err = hub.SaveRawHubMsg(h.ID, h.Scope, "status", statusData)
		if err != nil {
			log.Warningf("spn/docks: failed to save raw status msg: %w", err)
		}
	}

	return h, forward, nil
}

func verifyHubIP(ctx context.Context, h *hub.Hub, ip net.IP) error {
	// Create connection.
	ship, err := ships.Launch(ctx, h, nil, ip)
	if err != nil {
		return fmt.Errorf("failed to launch ship to %s: %s", ip, err)
	}

	// Start crane for receiving reply.
	crane, err := NewCrane(ctx, ship, h, nil)
	if err != nil {
		return fmt.Errorf("failed to create crane: %w", err)
	}
	module.StartWorker("crane unloader", crane.unloader)
	defer crane.Stop(nil)

	// Verify Hub.
	err = crane.VerifyConnectedHub()
	if err != nil {
		return err
	}

	// End connection.
	tErr := crane.endInit()
	if tErr != nil {
		log.Debugf("spn/docks: failed to end verification connection to %s: %s", ip, tErr)
	}

	return nil
}
