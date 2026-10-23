// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package fleetapihelpers

import (
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// DNSClaimResourceID identifies a claim independently of customer subscription or stamp.
func DNSClaimResourceID(baseDomain string) (*azcorearm.ResourceID, error) {
	slug, zone, found := strings.Cut(strings.ToLower(baseDomain), ".")
	if !found || len(slug) == 0 || len(zone) == 0 {
		return nil, utils.TrackError(fmt.Errorf("invalid DNS base domain %q", baseDomain))
	}
	resourceID, err := azcorearm.ParseResourceID("/providers/" + coreapi.ProviderNamespace + "/dnsZones/" + zone + "/dnsClaims/" + slug)
	return resourceID, utils.TrackError(err)
}
