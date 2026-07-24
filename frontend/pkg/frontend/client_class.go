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

	// Bound label size so a spoofed header cannot create huge series keys.
	maxUserAgentLabelLen = 256
)

// userAgentMetricLabel returns a Prometheus label value for the request User-Agent.
// Only known CapZ/ASO product tokens (including versions) are kept so client
// versions remain visible without accepting arbitrary header values.
// Everything else collapses to "other".
func userAgentMetricLabel(ua string) string {
	var parts []string
	for _, tok := range strings.Fields(ua) {
		if strings.HasPrefix(tok, userAgentTokenASO) || strings.HasPrefix(tok, userAgentTokenCAPZ) {
			parts = append(parts, tok)
		}
	}
	if len(parts) == 0 {
		return userAgentOther
	}

	label := strings.Join(parts, " ")
	if len(label) > maxUserAgentLabelLen {
		return userAgentOther
	}
	return label
}
