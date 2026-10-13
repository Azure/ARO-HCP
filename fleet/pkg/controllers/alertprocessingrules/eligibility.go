// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package alertprocessingrules

import (
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertprocessingrules/armalertprocessingrules"
)

const (
	// PurposeTagKey is the tag key ARO HCP stamps on every resource it owns.
	PurposeTagKey = "aroHCPPurpose"

	// PurposeTagValue is the tag value identifying an alert processing rule created
	// for temporary alert suppression. Only rules carrying this exact key/value pair
	// are considered for reaping; anything else in the resource group is left alone,
	// including rules created by hand during an incident.
	PurposeTagValue = "alert-processing-rule"

	// maxTimeZoneSkew is the largest offset between any wall-clock time and UTC
	// (real-world zones span UTC-12 to UTC+14).
	//
	// Schedule.EffectiveUntil is an ISO-8601 timestamp without a timezone suffix,
	// interpreted by Azure in Schedule.TimeZone (defaulting to UTC). Schedule.TimeZone
	// uses Windows timezone IDs ("Pacific Standard Time"), which Go's time.LoadLocation
	// cannot resolve, and the container image is not guaranteed to ship tzdata. Rather
	// than guess, we treat a non-UTC schedule's deadline as up to maxTimeZoneSkew later
	// than it reads, so we always err towards keeping a rule that might still be in
	// effect. Deleting a live suppression rule would unmask alerts; keeping a dead one
	// for an extra few hours costs one slot out of the 1000-per-subscription budget.
	maxTimeZoneSkew = 14 * time.Hour
)

// effectiveUntilLayouts are the timestamp formats accepted for
// Schedule.EffectiveUntil, most-expected first.
//
// Azure documents the field as ISO-8601 "without timezone suffix", but we parse the
// offset-bearing forms too so that a rule written by a client that includes one is
// still evaluated rather than skipped. Go's time.Parse accepts an optional fractional
// second after the seconds field even when the layout omits it, so these five layouts
// also cover the fractional variants.
var effectiveUntilLayouts = []struct {
	layout string
	// hasOffset reports whether the layout pins the timestamp to an absolute
	// instant, making the Schedule.TimeZone skew allowance unnecessary.
	hasOffset bool
}{
	{layout: "2006-01-02T15:04:05", hasOffset: false},
	{layout: time.RFC3339, hasOffset: true},
	{layout: "2006-01-02T15:04", hasOffset: false},
	{layout: "2006-01-02 15:04:05", hasOffset: false},
	{layout: "2006-01-02", hasOffset: false},
}

// decision records whether a rule should be reaped, and why. The reason is logged
// either way so an operator can tell a rule that was skipped from one that was
// never considered.
type decision struct {
	reap   bool
	reason string
}

func skip(format string, args ...any) decision {
	return decision{reap: false, reason: fmt.Sprintf(format, args...)}
}

func reap(format string, args ...any) decision {
	return decision{reap: true, reason: fmt.Sprintf(format, args...)}
}

// evaluate decides whether a single alert processing rule is eligible for reaping.
//
// A rule is reaped only when all three criteria hold:
//
//  1. it carries the aroHCPPurpose=alert-processing-rule tag,
//  2. it has no recurrence, and
//  3. its effectiveUntil is older than expiryThreshold before now.
//
// Anything we cannot positively evaluate — a missing schedule, an absent or
// unparseable effectiveUntil, a rule with no name — is skipped rather than deleted.
// This is deliberately asymmetric: a rule left behind is reclaimed on a later pass
// once the ambiguity is resolved, whereas a rule deleted in error silently unmasks
// the alerts it was suppressing.
func evaluate(rule *armalertprocessingrules.AlertProcessingRule, now time.Time, expiryThreshold time.Duration) decision {
	if rule == nil {
		return skip("rule is nil")
	}
	if rule.Name == nil || len(*rule.Name) == 0 {
		return skip("rule has no name, so it cannot be addressed for deletion")
	}

	if value, ok := rule.Tags[PurposeTagKey]; !ok || value == nil || *value != PurposeTagValue {
		return skip("rule does not carry the %s=%s tag", PurposeTagKey, PurposeTagValue)
	}

	if rule.Properties == nil {
		return skip("rule has no properties, so its schedule cannot be evaluated")
	}

	schedule := rule.Properties.Schedule
	if schedule == nil {
		return skip("rule has no schedule, so it never expires")
	}

	if len(schedule.Recurrences) > 0 {
		return skip("rule is recurring (%d recurrence(s)), so it is not a one-shot suppression", len(schedule.Recurrences))
	}

	if schedule.EffectiveUntil == nil || len(*schedule.EffectiveUntil) == 0 {
		return skip("rule has no effectiveUntil, so it never expires")
	}

	effectiveUntil, hasOffset, err := parseEffectiveUntil(*schedule.EffectiveUntil)
	if err != nil {
		return skip("rule has an unparseable effectiveUntil: %v", err)
	}

	// Only naive timestamps need the skew allowance; an explicit offset already
	// identifies an absolute instant.
	if !hasOffset && !isUTC(schedule.TimeZone) {
		effectiveUntil = effectiveUntil.Add(maxTimeZoneSkew)
	}

	deadline := now.Add(-expiryThreshold)
	if !effectiveUntil.Before(deadline) {
		return skip("rule expires at %s, which is not yet older than the %s expiry threshold",
			effectiveUntil.Format(time.RFC3339), expiryThreshold)
	}

	return reap("rule expired at %s, more than %s ago", effectiveUntil.Format(time.RFC3339), expiryThreshold)
}

// parseEffectiveUntil parses a Schedule.EffectiveUntil value, reporting whether the
// parsed form carried an explicit UTC offset. Timestamps without an offset are
// returned as UTC, matching Azure's default schedule timezone.
func parseEffectiveUntil(value string) (time.Time, bool, error) {
	trimmed := strings.TrimSpace(value)
	for _, candidate := range effectiveUntilLayouts {
		parsed, err := time.Parse(candidate.layout, trimmed)
		if err != nil {
			continue
		}
		return parsed.UTC(), candidate.hasOffset, nil
	}
	return time.Time{}, false, fmt.Errorf("%q does not match any supported ISO-8601 layout", value)
}

// isUTC reports whether a Schedule.TimeZone needs no skew allowance. Azure defaults
// an unset schedule timezone to UTC.
func isUTC(timeZone *string) bool {
	if timeZone == nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(*timeZone)) {
	case "", "utc", "coordinated universal time":
		return true
	default:
		return false
	}
}
