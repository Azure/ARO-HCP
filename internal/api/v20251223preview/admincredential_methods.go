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

package v20251223preview

import (
	"github.com/Azure/ARO-HCP/internal/api/v20251223preview/generated"
	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	armresourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources/arm"
)

func newHCPOpenShiftClusterAdminCredential(from *resourcesapi.HCPOpenShiftClusterAdminCredential) *generated.HcpOpenShiftClusterAdminCredential {
	return &generated.HcpOpenShiftClusterAdminCredential{
		ExpirationTimestamp: resourcesapi.PtrOrNil(from.ExpirationTimestamp),
		Kubeconfig:          resourcesapi.PtrOrNil(from.Kubeconfig),
	}
}

func (v version) MarshalHCPOpenShiftClusterAdminCredential(from *resourcesapi.HCPOpenShiftClusterAdminCredential) ([]byte, error) {
	return armresourcesapi.MarshalJSON(newHCPOpenShiftClusterAdminCredential(from))
}
