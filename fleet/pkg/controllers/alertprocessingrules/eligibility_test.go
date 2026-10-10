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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertprocessingrules/armalertprocessingrules"
)

// now is the fixed "current time" all eligibility tests are evaluated against.
var now = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// testExpiryThreshold is the grace period used by the eligibility tests.
const testExpiryThreshold = time.Hour

// ruleOption mutates a rule built by newRule.
type ruleOption func(*armalertprocessingrules.AlertProcessingRule)

// newRule builds a rule that is eligible for reaping by default: correctly tagged,
// non-recurring, and expired well beyond the threshold. Each test then applies the
// single option that should flip the decision, so the test names describe exactly
// which criterion is under test.
func newRule(options ...ruleOption) *armalertprocessingrules.AlertProcessingRule {
	rule := &armalertprocessingrules.AlertProcessingRule{
		Name: ptr.To("suppress-cluster-abc"),
		Tags: map[string]*string{
			PurposeTagKey: ptr.To(PurposeTagValue),
		},
		Properties: &armalertprocessingrules.AlertProcessingRuleProperties{
			Schedule: &armalertprocessingrules.Schedule{
				// 24h before now, comfortably past the 1h threshold.
				EffectiveUntil: ptr.To("2026-10-05T12:00:00"),
			},
		},
	}
	for _, option := range options {
		option(rule)
	}
	return rule
}

func withEffectiveUntil(value string) ruleOption {
	return func(r *armalertprocessingrules.AlertProcessingRule) {
		r.Properties.Schedule.EffectiveUntil = ptr.To(value)
	}
}

func withTimeZone(value string) ruleOption {
	return func(r *armalertprocessingrules.AlertProcessingRule) {
		r.Properties.Schedule.TimeZone = ptr.To(value)
	}
}

func TestEvaluate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rule     *armalertprocessingrules.AlertProcessingRule
		expected bool
	}{
		{
			name:     "tagged, non-recurring and long expired is reaped",
			rule:     newRule(),
			expected: true,
		},

		// Criterion 1: the aroHCPPurpose tag.
		{
			name: "rule with no tags at all is left alone",
			rule: newRule(func(r *armalertprocessingrules.AlertProcessingRule) {
				r.Tags = nil
			}),
			expected: false,
		},
		{
			name: "rule tagged for another ARO HCP purpose is left alone",
			rule: newRule(func(r *armalertprocessingrules.AlertProcessingRule) {
				r.Tags = map[string]*string{PurposeTagKey: ptr.To("service")}
			}),
			expected: false,
		},
		{
			name: "rule with a nil tag value is left alone",
			rule: newRule(func(r *armalertprocessingrules.AlertProcessingRule) {
				r.Tags = map[string]*string{PurposeTagKey: nil}
			}),
			expected: false,
		},
		{
			name: "tag match is case sensitive",
			rule: newRule(func(r *armalertprocessingrules.AlertProcessingRule) {
				r.Tags = map[string]*string{PurposeTagKey: ptr.To("Alert-Processing-Rule")}
			}),
			expected: false,
		},

		// Criterion 2: no recurrence.
		{
			name: "recurring rule is left alone even though it is expired",
			rule: newRule(func(r *armalertprocessingrules.AlertProcessingRule) {
				r.Properties.Schedule.Recurrences = []armalertprocessingrules.RecurrenceClassification{
					&armalertprocessingrules.DailyRecurrence{
						RecurrenceType: ptr.To(armalertprocessingrules.RecurrenceTypeDaily),
					},
				}
			}),
			expected: false,
		},
		{
			name: "weekly recurring rule is left alone",
			rule: newRule(func(r *armalertprocessingrules.AlertProcessingRule) {
				r.Properties.Schedule.Recurrences = []armalertprocessingrules.RecurrenceClassification{
					&armalertprocessingrules.WeeklyRecurrence{
						RecurrenceType: ptr.To(armalertprocessingrules.RecurrenceTypeWeekly),
					},
				}
			}),
			expected: false,
		},

		// Criterion 3: effectiveUntil older than the threshold.
		{
			name:     "rule whose effectiveUntil is in the future is left alone",
			rule:     newRule(withEffectiveUntil("2026-10-07T12:00:00")),
			expected: false,
		},
		{
			name:     "rule expired but still inside the grace period is left alone",
			rule:     newRule(withEffectiveUntil("2026-10-06T11:30:00")),
			expected: false,
		},
		{
			name:     "rule expired exactly at the threshold boundary is left alone",
			rule:     newRule(withEffectiveUntil("2026-10-06T11:00:00")),
			expected: false,
		},
		{
			name:     "rule expired one second past the threshold boundary is reaped",
			rule:     newRule(withEffectiveUntil("2026-10-06T10:59:59")),
			expected: true,
		},

		// Schedules we cannot evaluate must never be reaped.
		{
			name: "rule with no properties is left alone",
			rule: newRule(func(r *armalertprocessingrules.AlertProcessingRule) {
				r.Properties = nil
			}),
			expected: false,
		},
		{
			name: "rule with no schedule never expires and is left alone",
			rule: newRule(func(r *armalertprocessingrules.AlertProcessingRule) {
				r.Properties.Schedule = nil
			}),
			expected: false,
		},
		{
			name: "rule with no effectiveUntil never expires and is left alone",
			rule: newRule(func(r *armalertprocessingrules.AlertProcessingRule) {
				r.Properties.Schedule.EffectiveUntil = nil
			}),
			expected: false,
		},
		{
			name:     "rule with an empty effectiveUntil is left alone",
			rule:     newRule(withEffectiveUntil("")),
			expected: false,
		},
		{
			name:     "rule with an unparseable effectiveUntil is left alone",
			rule:     newRule(withEffectiveUntil("not-a-timestamp")),
			expected: false,
		},
		{
			name: "rule with no name cannot be addressed and is left alone",
			rule: newRule(func(r *armalertprocessingrules.AlertProcessingRule) {
				r.Name = nil
			}),
			expected: false,
		},
		{
			name: "rule with an empty name is left alone",
			rule: newRule(func(r *armalertprocessingrules.AlertProcessingRule) {
				r.Name = ptr.To("")
			}),
			expected: false,
		},
		{
			name:     "nil rule is left alone",
			rule:     nil,
			expected: false,
		},

		// Timezone handling.
		{
			name:     "explicit UTC schedule needs no skew allowance",
			rule:     newRule(withEffectiveUntil("2026-10-06T10:00:00"), withTimeZone("UTC")),
			expected: true,
		},
		{
			name:     "unset timezone defaults to UTC and needs no skew allowance",
			rule:     newRule(withEffectiveUntil("2026-10-06T10:00:00")),
			expected: true,
		},
		{
			name: "non-UTC schedule within the timezone skew allowance is left alone",
			rule: newRule(
				withEffectiveUntil("2026-10-06T10:00:00"),
				withTimeZone("Pacific Standard Time"),
			),
			expected: false,
		},
		{
			name: "non-UTC schedule well beyond the timezone skew allowance is reaped",
			rule: newRule(
				withEffectiveUntil("2026-10-04T10:00:00"),
				withTimeZone("Pacific Standard Time"),
			),
			expected: true,
		},
		{
			name: "offset-bearing timestamp pins an instant and needs no skew allowance",
			rule: newRule(
				withEffectiveUntil("2026-10-06T10:00:00Z"),
				withTimeZone("Pacific Standard Time"),
			),
			expected: true,
		},
		{
			name: "offset-bearing timestamp is interpreted in its own offset",
			// 10:00 at UTC+14 is 20:00 UTC on 2026-10-05, i.e. expired.
			rule: newRule(
				withEffectiveUntil("2026-10-06T10:00:00+14:00"),
				withTimeZone("Pacific Standard Time"),
			),
			expected: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := evaluate(tc.rule, now, testExpiryThreshold)
			assert.Equal(t, tc.expected, d.reap, "unexpected reap decision; reason was %q", d.reason)
			assert.NotEmpty(t, d.reason, "every decision must carry a reason for logging")
		})
	}
}

// TestEvaluateZeroExpiryThreshold confirms that a zero threshold reaps anything
// already past its effectiveUntil, so operators can opt out of the grace period.
func TestEvaluateZeroExpiryThreshold(t *testing.T) {
	justExpired := newRule(withEffectiveUntil("2026-10-06T11:59:59"))
	assert.True(t, evaluate(justExpired, now, 0).reap, "a rule past its effectiveUntil should reap with no grace period")

	notYetExpired := newRule(withEffectiveUntil("2026-10-06T12:00:01"))
	assert.False(t, evaluate(notYetExpired, now, 0).reap, "a rule before its effectiveUntil should never reap")
}

func TestParseEffectiveUntil(t *testing.T) {
	for _, tc := range []struct {
		name              string
		value             string
		expected          time.Time
		expectedHasOffset bool
	}{
		{
			name:     "documented form without timezone suffix",
			value:    "2026-10-05T12:00:00",
			expected: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC),
		},
		{
			name:     "fractional seconds without timezone suffix",
			value:    "2026-10-05T12:00:00.500",
			expected: time.Date(2026, 10, 5, 12, 0, 0, 500000000, time.UTC),
		},
		{
			name:     "minute precision",
			value:    "2026-10-05T12:00",
			expected: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC),
		},
		{
			name:     "date only",
			value:    "2026-10-05",
			expected: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "space separated",
			value:    "2026-10-05 12:00:00",
			expected: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC),
		},
		{
			name:              "RFC3339 with Z",
			value:             "2026-10-05T12:00:00Z",
			expected:          time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC),
			expectedHasOffset: true,
		},
		{
			name:              "RFC3339 with numeric offset is normalised to UTC",
			value:             "2026-10-05T14:00:00+02:00",
			expected:          time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC),
			expectedHasOffset: true,
		},
		{
			name:     "surrounding whitespace is tolerated",
			value:    "  2026-10-05T12:00:00  ",
			expected: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed, hasOffset, err := parseEffectiveUntil(tc.value)
			require.NoError(t, err, "expected %q to parse", tc.value)
			assert.True(t, tc.expected.Equal(parsed), "expected %s, got %s", tc.expected, parsed)
			assert.Equal(t, tc.expectedHasOffset, hasOffset, "unexpected hasOffset for %q", tc.value)
		})
	}
}

func TestParseEffectiveUntilRejectsGarbage(t *testing.T) {
	for _, value := range []string{"", "not-a-timestamp", "2026-13-45T99:99:99", "1696593600"} {
		_, _, err := parseEffectiveUntil(value)
		assert.Error(t, err, "expected %q to be rejected", value)
	}
}

func TestIsUTC(t *testing.T) {
	for _, value := range []string{"", "UTC", "utc", " UTC ", "Coordinated Universal Time"} {
		assert.True(t, isUTC(ptr.To(value)), "expected %q to be treated as UTC", value)
	}
	assert.True(t, isUTC(nil), "an unset schedule timezone defaults to UTC")

	for _, value := range []string{"Pacific Standard Time", "GMT Standard Time", "Europe/Berlin"} {
		assert.False(t, isUTC(ptr.To(value)), "expected %q to require a skew allowance", value)
	}
}
