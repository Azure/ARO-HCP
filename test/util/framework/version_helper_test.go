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

func TestPickAtLeastOpenshiftVersionId(t *testing.T) {
	t.Parallel()

	// For examples of latest OpenShift versions, see OpenShift Release Status
	// page at https://openshift-release.apps.ci.l2s4.p1.openshiftapps.com/
	const (
		// examples of OCP Nightly versions
		nightly419 = "4.19.0-0.nightly-multi-2026-09-01-142156"
		nightly421 = "4.21.0-0.nightly-multi-2026-09-03-080000"
		nightly500 = "5.0.0-0.nightly-multi-2026-09-09-181844"
		// examples of OCP Dev Preview versions
		devprev419 = "4.19.0-ec.1"
		devprev421 = "4.21.0-ec.3"
		devprev500 = "5.0.0-ec.6"
		// examples of OCP Release Candidate versions
		rc419 = "4.19.0-rc.1"
		rc421 = "4.21.0-rc.3"
		rc500 = "5.0.0-rc.0"
	)

	tests := []struct {
		name           string
		defaultVersion string
		minimalVersion string
		wantVersion    string
		wantErr        bool
		wantSkippable  bool // error should satisfy IsIncompatibleNightlyVersionError
	}{
		//
		// nightly: defaultVersion satisfies the minimum
		//
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
			name:           "nightly default same minor satisfies patch-zero minimum",
			defaultVersion: nightly419,
			minimalVersion: "4.19.0",
			wantVersion:    nightly419,
		},
		{
			name:           "nightly default patch 0 does not satisfy patch-qualified minimum",
			defaultVersion: nightly419,
			minimalVersion: "4.19.1",
			wantErr:        true,
			wantSkippable:  true,
		},
		{
			name:           "nightly default higher major satisfies minimum",
			defaultVersion: nightly500,
			minimalVersion: "4.21",
			wantVersion:    nightly500,
		},

		//
		// dev preview: defaultVersion satisfies the minimum
		//
		{
			name:           "dev preview default higher minor satisfies minimum",
			defaultVersion: devprev421,
			minimalVersion: "4.19",
			wantVersion:    devprev421,
		},
		{
			name:           "dev prevew default same minor satisfies minimum",
			defaultVersion: devprev419,
			minimalVersion: "4.19",
			wantVersion:    devprev419,
		},
		{
			name:           "dev preview default same minor satisfies patch-zero minimum",
			defaultVersion: devprev419,
			minimalVersion: "4.19.0",
			wantVersion:    devprev419,
		},
		{
			name:           "dev preview default patch 0 does not satisfy patch-qualified minimum",
			defaultVersion: devprev419,
			minimalVersion: "4.19.1",
			wantErr:        true,
			wantSkippable:  true,
		},
		{
			name:           "dev preview default higher major satisfies minimum",
			defaultVersion: devprev500,
			minimalVersion: "4.21",
			wantVersion:    devprev500,
		},

		//
		// release candidate: defaultVersion satisfies the minimum
		//
		{
			name:           "rc default higher minor satisfies minimum",
			defaultVersion: rc421,
			minimalVersion: "4.19",
			wantVersion:    rc421,
		},
		{
			name:           "rc default same minor satisfies minimum",
			defaultVersion: rc419,
			minimalVersion: "4.19",
			wantVersion:    rc419,
		},
		{
			name:           "rc default same minor satisfies patch-zero minimum",
			defaultVersion: rc419,
			minimalVersion: "4.19.0",
			wantVersion:    rc419,
		},
		{
			name:           "rc default patch 0 does not satisfy patch-qualified minimum",
			defaultVersion: rc419,
			minimalVersion: "4.19.1",
			wantErr:        true,
			wantSkippable:  true,
		},
		{
			name:           "rc default higher major satisfies minimum",
			defaultVersion: rc500,
			minimalVersion: "4.21",
			wantVersion:    rc500,
		},

		//
		// nightly: defaultVersion does NOT satisfy the minimum → skippable error
		//
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

		//
		// rc: defaultVersion does NOT satisfy the minimum → skippable error
		//
		{
			name:           "rc default lower minor does not satisfy minimum",
			defaultVersion: rc500,
			minimalVersion: "5.1",
			wantErr:        true,
			wantSkippable:  true,
		},
		{
			name:           "rc default lower major does not satisfy minimum",
			defaultVersion: rc419,
			minimalVersion: "5.0",
			wantErr:        true,
			wantSkippable:  true,
		},

		//
		// stable: defaultVersion satisfies the minimum
		//
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

		//
		// stable: defaultVersion does NOT satisfy the minimum → fallback to minimal
		//
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

		//
		// bad inputs
		//
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

			got, err := PickAtLeastOpenshiftVersionId(tc.defaultVersion, tc.minimalVersion)

			if tc.wantErr {
				require.Error(t, err, "expected an error")
				assert.Empty(t, got, "version should be empty on error")
				if tc.wantSkippable {
					assert.True(t, IsIncompatibleNightlyVersionError(err),
						"error should satisfy IsIncompatibleNightlyVersionError that so test cases can Skip; got: %v", err)
					assert.True(t, errors.Is(err, ErrNightlyVersionTooOld),
						"nightly-too-old error should wrap ErrNightlyVersionTooOld); got: %v", err)
				} else {
					assert.False(t, IsIncompatibleNightlyVersionError(err),
						"parse error should not satisfy IsIncompatibleNightlyVersionError")
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantVersion, got)
		})
	}
}
