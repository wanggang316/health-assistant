package quota

import (
	"testing"
	"time"
)

func TestDay(t *testing.T) {
	tests := []struct {
		now    string
		offset int
		date   string
		resets string
	}{
		{"2026-11-22T04:12:00Z", 480, "2026-11-22", "2026-11-22T16:00:00Z"},
		{"2026-11-22T16:00:00Z", 480, "2026-11-23", "2026-11-23T16:00:00Z"},
		{"2026-11-22T10:00:00Z", -720, "2026-11-21", "2026-11-22T12:00:00Z"},
		{"2026-11-22T10:00:00Z", 840, "2026-11-23", "2026-11-23T10:00:00Z"},
		{"2026-11-22T23:59:59Z", 0, "2026-11-22", "2026-11-23T00:00:00Z"},
	}
	for _, tt := range tests {
		now, _ := time.Parse(time.RFC3339, tt.now)
		date, resets := Day(now, tt.offset)
		if date != tt.date || resets.Format(time.RFC3339) != tt.resets {
			t.Errorf("Day(%s, %d) = %s, %s; want %s, %s", tt.now, tt.offset, date, resets.Format(time.RFC3339), tt.date, tt.resets)
		}
	}
}
