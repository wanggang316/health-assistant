package localtime

import (
	"testing"
	"time"
)

func TestDateAndMealType(t *testing.T) {
	tests := []struct {
		utc    string
		offset int
		date   string
		meal   string
	}{
		{"2026-11-22T04:12:00Z", 480, "2026-11-22", Lunch},     // 12:12 Shanghai
		{"2026-11-21T23:30:00Z", 480, "2026-11-22", Breakfast}, // 07:30
		{"2026-11-22T02:30:00Z", 480, "2026-11-22", Lunch},     // 10:30 boundary
		{"2026-11-22T06:30:00Z", 480, "2026-11-22", Snack},     // 14:30 boundary
		{"2026-11-22T13:29:00Z", 480, "2026-11-22", Dinner},    // 21:29
		{"2026-11-22T13:30:00Z", 480, "2026-11-22", Snack},     // 21:30
		{"2026-11-22T11:00:00Z", -720, "2026-11-21", Snack},    // 23:00 previous day
		{"2026-11-22T11:00:00Z", 840, "2026-11-23", Snack},     // 01:00 next day
	}
	for _, tt := range tests {
		ts, _ := time.Parse(time.RFC3339, tt.utc)
		if got := Date(ts, tt.offset); got != tt.date {
			t.Errorf("Date(%s, %d) = %s, want %s", tt.utc, tt.offset, got, tt.date)
		}
		if got := SuggestMealType(ts, tt.offset); got != tt.meal {
			t.Errorf("SuggestMealType(%s, %d) = %s, want %s", tt.utc, tt.offset, got, tt.meal)
		}
	}
	if !ValidMealType("lunch") || ValidMealType("brunch") || ValidOffset(841) {
		t.Error("validators")
	}
}
