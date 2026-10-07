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

package fleetapihelpers

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestRolloutVersionFromName(t *testing.T) {
	for _, group := range []string{"stable", "fast", "candidate", "nightly"} {
		profile, err := RolloutVersionFromName(group + "-4.21")
		require.NoError(t, err)
		require.Empty(t, cmp.Diff(coreapi.VersionProfile{ID: "4.21", ChannelGroup: group}, profile))
	}
	for _, name := range []string{"", "unsupported-4.21", "stable-4.21.0", "stable-04.21", "stable-4.021", "stable-18446744073709551616.21"} {
		_, err := RolloutVersionFromName(name)
		require.Error(t, err, "invalid rollout name %q", name)
	}
}
