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

package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestRecordPhaseEntry_FirstEntryWins(t *testing.T) {
	op := &Operation{}
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(10 * time.Second)

	op.RecordPhaseEntry(arm.ProvisioningStateAccepted, t0)
	op.RecordPhaseEntry(arm.ProvisioningStateAccepted, t1)

	require.Len(t, op.PhaseTimestamps, 1)
	require.True(t, op.PhaseTimestamps[arm.ProvisioningStateAccepted].Equal(t0),
		"second call must not overwrite first recorded timestamp")
}

func TestRecordPhaseEntry_SkipsEmptyPhaseOrZeroTime(t *testing.T) {
	op := &Operation{}

	op.RecordPhaseEntry("", time.Now())
	require.Nil(t, op.PhaseTimestamps, "empty phase must be a no-op")

	op.RecordPhaseEntry(arm.ProvisioningStateAccepted, time.Time{})
	require.Nil(t, op.PhaseTimestamps, "zero time must be a no-op")
}

func TestRecordPhaseEntry_RecordsMultiplePhases(t *testing.T) {
	op := &Operation{}
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	op.RecordPhaseEntry(arm.ProvisioningStateAccepted, t0)
	op.RecordPhaseEntry(arm.ProvisioningStateProvisioning, t0.Add(30*time.Second))
	op.RecordPhaseEntry(arm.ProvisioningStateSucceeded, t0.Add(5*time.Minute))

	require.Len(t, op.PhaseTimestamps, 3)
	require.True(t, op.PhaseTimestamps[arm.ProvisioningStateAccepted].Equal(t0))
	require.True(t, op.PhaseTimestamps[arm.ProvisioningStateProvisioning].Equal(t0.Add(30*time.Second)))
	require.True(t, op.PhaseTimestamps[arm.ProvisioningStateSucceeded].Equal(t0.Add(5*time.Minute)))
}

// TestOperation_PhaseTimestampsRoundTrip verifies JSON serialization preserves
// the map. This is the Cosmos persistence path.
func TestOperation_PhaseTimestampsRoundTrip(t *testing.T) {
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := &Operation{
		Status: arm.ProvisioningStateSucceeded,
		PhaseTimestamps: PhaseTimestampMap{
			arm.ProvisioningStateAccepted:     t0,
			arm.ProvisioningStateProvisioning: t0.Add(30 * time.Second),
			arm.ProvisioningStateSucceeded:    t0.Add(5 * time.Minute),
		},
	}

	buf, err := json.Marshal(op)
	require.NoError(t, err)

	var decoded Operation
	require.NoError(t, json.Unmarshal(buf, &decoded))

	require.Len(t, decoded.PhaseTimestamps, 3)
	require.True(t, decoded.PhaseTimestamps[arm.ProvisioningStateAccepted].Equal(t0))
	require.True(t, decoded.PhaseTimestamps[arm.ProvisioningStateProvisioning].Equal(t0.Add(30*time.Second)))
	require.True(t, decoded.PhaseTimestamps[arm.ProvisioningStateSucceeded].Equal(t0.Add(5*time.Minute)))
}

// TestOperation_PhaseTimestampsOmitEmpty verifies the field is omitted from
// JSON when empty, so existing Cosmos docs need no migration.
func TestOperation_PhaseTimestampsOmitEmpty(t *testing.T) {
	op := &Operation{Status: arm.ProvisioningStateAccepted}
	buf, err := json.Marshal(op)
	require.NoError(t, err)
	require.NotContains(t, string(buf), "phaseTimestamps")
}

func TestPhaseTimestampMap_DeepCopy(t *testing.T) {
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	src := PhaseTimestampMap{arm.ProvisioningStateAccepted: t0}

	dst := src.DeepCopy()
	dst[arm.ProvisioningStateProvisioning] = t0.Add(1 * time.Minute)

	require.Len(t, src, 1, "original map must not be mutated by DeepCopy")
	require.Len(t, dst, 2)
}

func TestPhaseTimestampMap_DeepCopyNil(t *testing.T) {
	var src PhaseTimestampMap
	require.Nil(t, src.DeepCopy())
}

// TestRecordPhaseEntry_TransitionOrdering documents the expected call pattern
// at transition sites. The helper requires callers to pass phase and time
// explicitly, so the "assign LastTransitionTime before Status" ordering that
// the real writers use cannot cause the wrong phase to be recorded.
func TestRecordPhaseEntry_TransitionOrdering(t *testing.T) {
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	op := &Operation{Status: arm.ProvisioningStateAccepted, LastTransitionTime: t0}
	op.RecordPhaseEntry(op.Status, op.LastTransitionTime)

	// Simulate a real transition site: timestamp first, then status, then
	// helper with the explicit new values.
	op.LastTransitionTime = t0.Add(30 * time.Second)
	op.Status = arm.ProvisioningStateProvisioning
	op.RecordPhaseEntry(op.Status, op.LastTransitionTime)

	op.LastTransitionTime = t0.Add(5 * time.Minute)
	op.Status = arm.ProvisioningStateSucceeded
	op.RecordPhaseEntry(op.Status, op.LastTransitionTime)

	require.Len(t, op.PhaseTimestamps, 3)
	require.True(t, op.PhaseTimestamps[arm.ProvisioningStateAccepted].Equal(t0))
	require.True(t, op.PhaseTimestamps[arm.ProvisioningStateProvisioning].Equal(t0.Add(30*time.Second)))
	require.True(t, op.PhaseTimestamps[arm.ProvisioningStateSucceeded].Equal(t0.Add(5*time.Minute)))
}
