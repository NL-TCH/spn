package hub

import "errors"

var (
	// ErrMissingInfo signifies that the hub is missing the HubAnnouncement.
	ErrMissingInfo = errors.New("hub is missing Announcement")

	// ErrMissingTransports signifies that the hub announcement did not specify any transports.
	ErrMissingTransports = errors.New("hub announcement is missing transports")

	// ErrMissingIPs signifies that the hub announcement did not specify any IPs,
	// or none of the IPs is supported by the client.
	ErrMissingIPs = errors.New("hub announcement is missing (supported) IPs")

	// ErrTemporaryValidationError is returned when a validation error might be temporary.
	ErrTemporaryValidationError = errors.New("temporary validation error")

	// ErrOldData is returned when received data is outdated.
	ErrOldData = errors.New("")
)
