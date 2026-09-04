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

package framework

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPickLatestOpenshiftVersionId(t *testing.T) {
	t.Parallel()

	const (
		nightly419 = "4.19.0-0.nightly-multi-2026-09-01-142156"
		nightly421 = "4.21.0-0.nightly-multi-2026-09-03-080000"
		nightly500 = "5.0.0-0.nightly-multi-2026-08-28-153004"
	)

	tests := []struct {
		name           string
		defaultVersion string
		minimalVersion string
		wantVersion    string
		wantErr        bool
		wantSkippable  bool // error should satisfy IsVersionNotFoundError
	}{
		// --- nightly: defaultVersion satisfies the minimum ---
		{
			name:           "nightly default higher minor satisfies minimum",
			defaultVersion: nightly421,
			minimalVersion: "4.19",
			wantVersion:    nightly421,
		},
		{
			name:           "nightly default same minor satisfies minimum",
			defaultVersion: nightly419,
			minimalVersion: "4.19",
			wantVersion:    nightly419,
		},
		{
			name:           "nightly default same minor satisfies patch-qualified minimum",
			defaultVersion: nightly419,
			minimalVersion: "4.19.0",
			wantVersion:    nightly419,
		},
		{
			name:           "nightly default higher major satisfies minimum",
			defaultVersion: nightly500,
			minimalVersion: "4.21",
			wantVersion:    nightly500,
		},

		// --- nightly: defaultVersion does NOT satisfy the minimum → skippable error ---
		{
			name:           "nightly default lower minor does not satisfy minimum",
			defaultVersion: nightly500,
			minimalVersion: "5.1",
			wantErr:        true,
			wantSkippable:  true,
		},
		{
			name:           "nightly default lower major does not satisfy minimum",
			defaultVersion: nightly419,
			minimalVersion: "5.0",
			wantErr:        true,
			wantSkippable:  true,
		},

		// --- non-nightly: defaultVersion satisfies the minimum ---
		{
			name:           "candidate default higher version satisfies minimum",
			defaultVersion: "4.21.5",
			minimalVersion: "4.19.3",
			wantVersion:    "4.21.5",
		},
		{
			name:           "candidate default equal version satisfies minimum",
			defaultVersion: "4.19.3",
			minimalVersion: "4.19.3",
			wantVersion:    "4.19.3",
		},
		{
			name:           "candidate default higher patch satisfies minimum",
			defaultVersion: "4.19.5",
			minimalVersion: "4.19.3",
			wantVersion:    "4.19.5",
		},

		// --- non-nightly: defaultVersion does NOT satisfy the minimum → fallback to minimal ---
		{
			name:           "candidate default lower minor falls back to minimal",
			defaultVersion: "4.18.5",
			minimalVersion: "4.19.3",
			wantVersion:    "4.19.3",
		},
		{
			name:           "candidate default lower patch falls back to minimal",
			defaultVersion: "4.19.2",
			minimalVersion: "4.19.3",
			wantVersion:    "4.19.3",
		},

		// --- bad inputs ---
		{
			name:           "unparseable defaultVersion",
			defaultVersion: "not-a-version",
			minimalVersion: "4.19",
			wantErr:        true,
			wantSkippable:  false,
		},
		{
			name:           "unparseable minimalVersion",
			defaultVersion: nightly419,
			minimalVersion: "not-a-version",
			wantErr:        true,
			wantSkippable:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := PickLatestOpenshiftVersionId(tc.defaultVersion, tc.minimalVersion)

			if tc.wantErr {
				require.Error(t, err, "expected an error")
				assert.Empty(t, got, "version should be empty on error")
				if tc.wantSkippable {
					assert.True(t, IsVersionNotFoundError(err),
						"error should satisfy IsVersionNotFoundError so test cases can Skip; got: %v", err)
					assert.True(t, errors.Is(err, ErrNoAcceptedNightlyTags),
						"nightly-too-old error should wrap ErrNoAcceptedNightlyTags; got: %v", err)
				} else {
					assert.False(t, IsVersionNotFoundError(err),
						"parse error should not satisfy IsVersionNotFoundError")
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantVersion, got)
		})
	}
}
