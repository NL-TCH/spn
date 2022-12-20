package unit

import (
	"github.com/tevino/abool"
)

// Unit describes a "work unit" and is meant to be embedded into another struct
// used for passing data moving through multiple processing steps.
type Unit struct {
	id           int64
	scheduler    *Scheduler
	epoch        int32
	finished     abool.AtomicBool
	highPriority abool.AtomicBool
	paused       abool.AtomicBool
}

// NewUnit returns a new unit within the scheduler.
func (s *Scheduler) NewUnit() *Unit {
	return &Unit{
		id:        s.currentUnitID.Add(1),
		epoch:     s.epoch.Load(),
		scheduler: s,
	}
}

// ReUseUnit re-initilizes the unit to be able to reuse already allocated structs.
func (u *Unit) ReUseUnit() {
	// Finish previous unit.
	u.FinishUnit()

	// Get new ID and unset finish flag.
	u.id = u.scheduler.currentUnitID.Add(1)
	u.finished.UnSet()
}

// WaitForUnitSlot blocks until the unit may be processed.
func (u *Unit) WaitForUnitSlot() {
	// Unpause.
	u.unpause()

	// High priority units may always process.
	if u.highPriority.IsSet() {
		return
	}

	for {
		// Check if we are allowed to process in the current slot.
		if u.id <= u.scheduler.clearanceUpTo.Load() {
			return
		}

		// Debug logging:
		// fmt.Printf("unit %d waiting for clearance at %d\n", u.id, u.scheduler.clearanceUpTo.Load())

		// Wait for next slot.
		<-u.scheduler.nextSlotSignal()
	}
}

// FinishUnit signals the unit scheduler that this unit has finished processing.
// Will no-op if called on a nil Unit.
func (u *Unit) FinishUnit() {
	if u == nil {
		return
	}

	// Always increase finished, even if the unit is from a previous epoch.
	if u.finished.SetToIf(false, true) {
		if u.epoch == u.scheduler.epoch.Load() {
			// If in same epoch, finish in epoch.
			u.scheduler.finished.Add(1)
		} else {
			// If not in same epoch, just increase the total counter.
			u.scheduler.finishedTotal.Add(1)
		}
	}
	u.RemoveUnitPriority()
	u.unpause()
}

// PauseUnit signals the unit scheduler that this unit is paused and not being
// processed at the moment. May only be called if WaitForUnitSlot() was called
// at least once.
func (u *Unit) PauseUnit() {
	// Only change if within the origin epoch.
	if u.epoch != u.scheduler.epoch.Load() {
		return
	}

	if u.finished.IsNotSet() && u.paused.SetToIf(false, true) {
		u.scheduler.pausedUnits.Add(1)

		// Increase clearance by one if unit is paused, as now another unit can take its slot.
		u.scheduler.clearanceUpTo.Add(1)
	}
}

// unpause signals the unit scheduler that this unit is not paused anymore and
// is now waiting for processing.
func (u *Unit) unpause() {
	// Only change if within the origin epoch.
	if u.epoch != u.scheduler.epoch.Load() {
		return
	}

	if u.paused.SetToIf(true, false) {
		if u.scheduler.pausedUnits.Add(-1) < 0 {
			// If we are within an epoch change, revert change and return.
			u.scheduler.pausedUnits.Add(1)
			return
		}

		// Reduce clearance by one if paused unit is woken up, as the previously paused unit requires a slot.
		// A paused unit is expected to already have been allowed to process once.
		u.scheduler.clearanceUpTo.Add(-1)
	}
}

// MakeUnitHighPriority marks the unit as high priority.
func (u *Unit) MakeUnitHighPriority() {
	// Only change if within the origin epoch.
	if u.epoch != u.scheduler.epoch.Load() {
		return
	}

	if u.finished.IsNotSet() && u.highPriority.SetToIf(false, true) {
		u.scheduler.highPrioUnits.Add(1)
	}
}

// IsHighPriorityUnit returns whether the unit has high priority.
func (u *Unit) IsHighPriorityUnit() bool {
	return u.highPriority.IsSet()
}

// RemoveUnitPriority removes the high priority mark.
func (u *Unit) RemoveUnitPriority() {
	// Only change if within the origin epoch.
	if u.epoch != u.scheduler.epoch.Load() {
		return
	}

	if u.highPriority.SetToIf(true, false) {
		if u.scheduler.highPrioUnits.Add(-1) < 0 {
			// If we are within an epoch change, revert change.
			u.scheduler.highPrioUnits.Add(1)
		}
	}
}
