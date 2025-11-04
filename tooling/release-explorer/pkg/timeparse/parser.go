package timeparse

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// ParseDuration parses a duration string with support for days and weeks.
// First tries standard library time.ParseDuration (supports h, m, s, ms, us, ns).
// Falls back to custom parsing for "d" (days) and "w" (weeks).
//
// Examples:
//   - "2h"    -> 2 hours (stdlib)
//   - "30m"   -> 30 minutes (stdlib)
//   - "1d"    -> 24 hours
//   - "2w"    -> 336 hours
//   - "1h30m" -> 1.5 hours (stdlib)
func ParseDuration(s string) (time.Duration, error) {
	// Try standard library first (supports h, m, s, ms, us, ns)
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}

	// Handle days and weeks with simple regex
	re := regexp.MustCompile(`^(\d+)([dw])$`)
	matches := re.FindStringSubmatch(s)

	if len(matches) != 3 {
		return 0, fmt.Errorf("invalid duration: %s (expected format: 2h, 30m, 1d, 2w, etc.)", s)
	}

	value, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, fmt.Errorf("invalid number in duration: %s", matches[1])
	}

	switch matches[2] {
	case "d":
		return time.Duration(value) * 24 * time.Hour, nil
	case "w":
		return time.Duration(value) * 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid duration unit: %s", matches[2])
	}
}

// ParseTimeToUTC parses a time string into a timestamp.
// Supports:
//   - RFC 3339 format: "2025-11-02T15:30:00Z" or "2025-11-02T15:30:00-05:00"
//   - Date only: "2025-11-02" (defaults to T00:00:00 in specified timezone)
//   - Duration: "1d", "2w", "12h" (relative to now, going back in time)
//
// All times are returned in UTC
// RFC 3339 format with explicit timezone overrides the useUTC setting.
func ParseTimeToUTC(timeStr string) (time.Time, error) {

	// Try RFC 3339 format first (includes timezone information)
	if t, err := time.Parse(time.RFC3339, timeStr); err == nil {
		return t, nil
	}

	// Try date-only format (YYYY-MM-DD) - default to 00:00:00
	dateOnlyRe := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	if dateOnlyRe.MatchString(timeStr) {
		// Append time component and parse as RFC3339
		fullTimeStr := timeStr + "T00:00:00Z"
		if t, err := time.Parse(time.RFC3339, fullTimeStr); err == nil {
			return t.UTC(), nil
		}
	}

	// Try parsing as duration (relative to now)
	duration, err := ParseDuration(timeStr)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid time: %s (expected RFC3339, YYYY-MM-DD, or duration like 1d, 2w, 12h)", timeStr)
	}

	// Calculate timestamp by going back from now
	return time.Now().UTC().Add(-duration), nil
}

// FormatRelativeTime formats a duration into a human-readable relative time string.
func FormatRelativeTime(d time.Duration) string {
	if d < time.Minute {
		return "less than a minute"
	}

	if d < time.Hour {
		minutes := int(d.Minutes())
		if minutes == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", minutes)
	}

	if d < 24*time.Hour {
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	}

	days := int(d.Hours() / 24)
	if days == 1 {
		return "1 day"
	}
	if days <= 28 {
		return fmt.Sprintf("%d days", days)
	}

	weeks := days / 7
	if weeks == 1 {
		return "1 week"
	}
	if weeks < 4 {
		return fmt.Sprintf("%d weeks", weeks)
	}

	months := days / 30
	if months == 1 {
		return "1 month"
	}
	if months < 12 {
		return fmt.Sprintf("%d months", months)
	}

	years := days / 365
	if years == 1 {
		return "1 year"
	}
	return fmt.Sprintf("%d years", years)
}
