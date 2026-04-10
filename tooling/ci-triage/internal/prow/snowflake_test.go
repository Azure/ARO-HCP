// Copyright 2025 Microsoft Corporation
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

package prow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildIDToISO(t *testing.T) {
	// Known Prow build ID should decode to a reasonable timestamp
	iso, err := BuildIDToISO("1900000000000000000")
	require.NoError(t, err)
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}$`, iso)
}

func TestSnowflakeRoundtrip(t *testing.T) {
	original := "2025-04-01T12:30:00"
	bid, err := ISOToBuildID(original)
	require.NoError(t, err)

	iso, err := BuildIDToISO(bid)
	require.NoError(t, err)
	assert.Equal(t, original, iso)
}

func TestSnowflakeOrdering(t *testing.T) {
	bid1, err := ISOToBuildID("2025-04-01")
	require.NoError(t, err)

	bid2, err := ISOToBuildID("2025-04-02")
	require.NoError(t, err)

	assert.Less(t, bid1, bid2, "earlier date should produce smaller build ID")
}

func TestBuildIDToISODateOnly(t *testing.T) {
	bid, err := ISOToBuildID("2025-04-01")
	require.NoError(t, err)

	iso, err := BuildIDToISO(bid)
	require.NoError(t, err)
	assert.Equal(t, "2025-04-01T00:00:00", iso)
}

func TestBuildIDToTimeInvalid(t *testing.T) {
	_, err := BuildIDToTime("not-a-number")
	assert.Error(t, err)
}
