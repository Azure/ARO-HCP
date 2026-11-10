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

package cifailures

import "regexp"

var (
	persistentJobEnvironment = regexp.MustCompile(`^(?:pull|branch|periodic)-ci-Azure-ARO-HCP-main-(?:e2e-|periodic-)?(integration|stage|prod)(?:-canary-arm-endpoint)?-e2e-parallel(?:-.+)?$`)
	developmentJob           = regexp.MustCompile(`^(?:pull|branch)-ci-Azure-ARO-HCP-main-e2e-parallel(?:-.+)?$`)
)

// jobEnvironment follows the CIHealth source lane conventions, including their
// presubmit and canary variants. There is no endpoint/region/default-env inference.
// Unknown job families remain visible when the job-name filter is broadened.
func jobEnvironment(name string) string {
	if match := persistentJobEnvironment.FindStringSubmatch(name); match != nil {
		switch match[1] {
		case "integration":
			return "int"
		case "stage":
			return "stg"
		case "prod":
			return "prod"
		}
	}
	if developmentJob.MatchString(name) {
		return "dev"
	}
	return "unknown"
}
