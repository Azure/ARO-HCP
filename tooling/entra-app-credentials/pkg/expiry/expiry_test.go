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

package expiry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestClassify(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	warning := 90 * 24 * time.Hour

	for name, test := range map[string]struct {
		end  time.Time
		want State
	}{
		"long before expiry":    {end: now.Add(warning + time.Second), want: Valid},
		"exactly at the window": {end: now.Add(warning), want: Expiring},
		"inside the window":     {end: now.Add(24 * time.Hour), want: Expiring},
		"one second left":       {end: now.Add(time.Second), want: Expiring},
		"expires right now":     {end: now, want: Expired},
		"expired in the past":   {end: now.Add(-time.Hour), want: Expired},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, test.want, Classify(test.end, now, warning))
		})
	}
}

func TestClassifyWithoutWarningWindowOnlyReportsExpiry(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	assert.Equal(t, Valid, Classify(now.Add(time.Hour), now, 0))
	assert.Equal(t, Expired, Classify(now, now, 0))
}

func TestDays(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	assert.Equal(t, 29, Days(now.Add(29*24*time.Hour+23*time.Hour), now))
	assert.Equal(t, 0, Days(now.Add(time.Hour), now))
	assert.Equal(t, -2, Days(now.Add(-2*24*time.Hour), now))
}
