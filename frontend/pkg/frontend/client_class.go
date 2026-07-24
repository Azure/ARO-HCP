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

package frontend

import "strings"

// Known User-Agent product tokens emitted by ASO and CapZ when calling Azure ARM / RP APIs.
// Examples observed in ARM HttpIncomingRequests:
//
//	aso-controller/v2.13.0-hcpclusters.9 cluster-api-provider-azure/v1.22.1-mce-217
const (
	userAgentTokenASO  = "aso-controller/"
	userAgentTokenCAPZ = "cluster-api-provider-azure/"
)

// userAgentMetricLabel returns a Prometheus label value for the request User-Agent.
// Known CapZ/ASO agents keep the (trimmed) User-Agent so versions remain visible;
// everything else collapses to "other" to bound cardinality.
func userAgentMetricLabel(ua string) string {
	ua = strings.Join(strings.Fields(ua), " ")
	if ua == "" {
		return userAgentOther
	}
	if strings.Contains(ua, userAgentTokenASO) || strings.Contains(ua, userAgentTokenCAPZ) {
		return ua
	}
	return userAgentOther
}
