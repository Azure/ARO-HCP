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

package versionrollout

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestInMemoryVersionAgeSource(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := clocktesting.NewFakeClock(start)
	src := NewInMemoryVersionAgeSource(clock)

	// mismatched cluster: desires 4.21.6, base is still 4.21.4
	upgrading := newTestSPC("c1", v("4.21.6"), []coreapi.HCPClusterActiveVersion{partial("4.21.6"), completed("4.21.4")}, nil)

	// first observation records "now": age 0, known.
	age, known := src.MismatchAge(upgrading)
	assert.True(t, known)
	assert.Equal(t, time.Duration(0), age)

	clock.Step(30 * time.Minute)
	age, known = src.MismatchAge(upgrading)
	assert.True(t, known)
	assert.Equal(t, 30*time.Minute, age, "mismatch age accumulates from first observation")

	// achieved age for the same cluster tracks its completed base version.
	achievedAge, known := src.AchievedAge(upgrading)
	assert.True(t, known)
	assert.Equal(t, time.Duration(0), achievedAge, "achieved base first observed now")

	// once achieved (base == desired), it is no longer mismatched.
	done := newTestSPC("c1", v("4.21.6"), []coreapi.HCPClusterActiveVersion{completed("4.21.6")}, nil)
	_, known = src.MismatchAge(done)
	assert.False(t, known, "achieved cluster is not mismatched")

	// achieved version changed (4.21.4 -> 4.21.6), so the achieved timer resets.
	achievedAge, known = src.AchievedAge(done)
	assert.True(t, known)
	assert.Equal(t, time.Duration(0), achievedAge)

	clock.Step(2 * time.Hour)
	achievedAge, known = src.AchievedAge(done)
	assert.True(t, known)
	assert.Equal(t, 2*time.Hour, achievedAge)
}
