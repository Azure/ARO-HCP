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

package ciruns

import "testing"

func TestJobEnvironmentFamilies(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"pull-ci-Azure-ARO-HCP-main-e2e-parallel", "dev"},
		{"pull-ci-Azure-ARO-HCP-main-e2e-parallel-inplace-upgrade", "dev"},
		{"pull-ci-Azure-ARO-HCP-main-integration-e2e-parallel-all", "int"},
		{"branch-ci-Azure-ARO-HCP-main-e2e-integration-e2e-parallel-all", "int"},
		{"periodic-ci-Azure-ARO-HCP-main-periodic-stage-e2e-parallel-ocp-nightly", "stg"},
		{"branch-ci-Azure-ARO-HCP-main-e2e-stage-e2e-parallel-all", "stg"},
		{"pull-ci-Azure-ARO-HCP-main-stage-canary-arm-endpoint-e2e-parallel", "stg"},
		{"branch-ci-Azure-ARO-HCP-main-e2e-prod-e2e-parallel", "prod"},
		{"pull-ci-Azure-ARO-HCP-main-prod-canary-arm-endpoint-e2e-parallel-slow", "prod"},
		{"unrelated-prod-e2e-parallel", "unknown"},
		{"pull-ci-Other-Repo-main-e2e-parallel", "unknown"},
		{"pull-ci-Azure-ARO-HCP-main-test-unit", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := JobEnvironment(tc.name); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
