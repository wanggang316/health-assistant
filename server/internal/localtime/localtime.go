// Package localtime turns a UTC time and a stored UTC offset into the local
// date and the default meal type (ADR 0011).
package localtime

import "time"

// Offset limits in minutes.
const (
	MinOffset = -720
	MaxOffset = 840
)

// ValidOffset reports whether a UTC offset in minutes is in range.
func ValidOffset(minutes int) bool { return minutes >= MinOffset && minutes <= MaxOffset }

// Local returns t shifted by the offset, in a UTC location, so that its
// clock fields are the local clock.
func Local(t time.Time, offsetMinutes int) time.Time {
	return t.UTC().Add(time.Duration(offsetMinutes) * time.Minute)
}

// Date returns the local date (YYYY-MM-DD) of t.
func Date(t time.Time, offsetMinutes int) string {
	return Local(t, offsetMinutes).Format(time.DateOnly)
}

// Meal types.
const (
	Breakfast = "breakfast"
	Lunch     = "lunch"
	Dinner    = "dinner"
	Snack     = "snack"
)

// MealTypes lists the valid meal types.
var MealTypes = []string{Breakfast, Lunch, Dinner, Snack}

// ValidMealType reports whether s is a meal type.
func ValidMealType(s string) bool {
	for _, m := range MealTypes {
		if s == m {
			return true
		}
	}
	return false
}

// SuggestMealType returns the default meal type for the local time:
// [04:00, 10:30) breakfast, [10:30, 14:30) lunch, [17:00, 21:30) dinner,
// else snack (§9.6).
func SuggestMealType(t time.Time, offsetMinutes int) string {
	l := Local(t, offsetMinutes)
	m := l.Hour()*60 + l.Minute()
	switch {
	case m >= 4*60 && m < 10*60+30:
		return Breakfast
	case m >= 10*60+30 && m < 14*60+30:
		return Lunch
	case m >= 17*60 && m < 21*60+30:
		return Dinner
	}
	return Snack
}
