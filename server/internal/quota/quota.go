// Package quota holds the recognition quota rules (§6.6).
package quota

import "time"

// Offset limits in minutes (ADR 0011).
const (
	MinOffsetMinutes = -720
	MaxOffsetMinutes = 840
)

// ValidOffset reports whether a UTC offset in minutes is in range.
func ValidOffset(minutes int) bool { return minutes >= MinOffsetMinutes && minutes <= MaxOffsetMinutes }

// Day returns the local date for the quota at now with the given UTC offset,
// and the UTC time when that local day ends.
func Day(now time.Time, offsetMinutes int) (date string, resetsAt time.Time) {
	off := time.Duration(offsetMinutes) * time.Minute
	local := now.UTC().Add(off)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	return midnight.Format(time.DateOnly), midnight.Add(24 * time.Hour).Add(-off)
}
