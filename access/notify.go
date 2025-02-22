package access

import (
	"fmt"
	"strings"
	"time"

	"github.com/safing/portbase/log"
	"github.com/safing/portbase/notifications"
)

const (
	day  = 24 * time.Hour
	week = 7 * day

	endOfPackageNearNotifID = "access:end-of-package-near"
)

func notifyOfPackageEnd(u *UserRecord) {
	// TODO: Check if subscription auto-renews.

	// Skip if there is not active subscription or if it has ended already.
	if u.Subscription == nil ||
		u.Subscription.EndsAt == nil ||
		time.Now().After(*u.Subscription.EndsAt) {
		return
	}

	// Calculate durations.
	sinceLastNotified := 52 * week // Never.
	if u.LastNotifiedOfEnd != nil {
		sinceLastNotified = time.Since(*u.LastNotifiedOfEnd)
	}
	untilEnd := time.Until(*u.Subscription.EndsAt)

	// Notify every two days in the week before end.
	notifType := notifications.Info
	switch {
	case untilEnd < week && sinceLastNotified > 2*day:
		// Notify 7, 5, 3 and 1 days before end.
		if untilEnd < 4*day {
			notifType = notifications.Warning
		}
		fallthrough

	case u.CurrentPlan != nil && u.CurrentPlan.Months >= 6 &&
		untilEnd < 4*week && sinceLastNotified > week:
		// Notify 4, 3 and 2 weeks before end - on long running packages.

		// Get names and messages.
		packageNameTitle := "Portmaster Package"
		if u.CurrentPlan != nil {
			packageNameTitle = u.CurrentPlan.Name
		}
		packageNameBody := packageNameTitle
		if !strings.HasSuffix(packageNameBody, " Package") {
			packageNameBody += " Package"
		}

		var endsText string
		daysUntilEnd := untilEnd / day
		switch daysUntilEnd { //nolint:exhaustive
		case 0:
			endsText = "today"
		case 1:
			endsText = "tomorrow"
		default:
			endsText = fmt.Sprintf("in %d days", daysUntilEnd)
		}

		var message string
		if u.CurrentPlan != nil && strings.HasSuffix(u.CurrentPlan.Name, "Supporter") {
			message = fmt.Sprintf(
				"Your current %s ends %s. Extend it to keep your benefits and continue your support of Portmaster.",
				packageNameBody,
				endsText,
			)
		} else {
			message = fmt.Sprintf(
				"Your current %s ends %s. Extend it to keep your full privacy protections.",
				packageNameBody,
				endsText,
			)
		}

		// Send notification.
		notifications.Notify(&notifications.Notification{
			EventID:      endOfPackageNearNotifID,
			Type:         notifType,
			Title:        fmt.Sprintf("%s About to Expire", packageNameTitle),
			Message:      message,
			ShowOnSystem: notifType == notifications.Warning,
			AvailableActions: []*notifications.Action{
				{
					Text:    "Open Account Page",
					Type:    notifications.ActionTypeOpenURL,
					Payload: "https://account.safing.io",
				},
				{
					ID:   "ack",
					Text: "Got it!",
				},
			},
		})

		// Save that we sent a notification.
		now := time.Now()
		u.LastNotifiedOfEnd = &now
		err := u.Save()
		if err != nil {
			log.Warningf("spn/access: failed to save user after sending subscription ending soon notification: %s", err)
		}
	}
}
