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
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/metadataapihelpers"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20251223preview/generated"
)

func newClusterAdminCredential(from *coreapi.ClusterAdminCredential) *generated.HcpOpenShiftClusterAdminCredential {
	return &generated.HcpOpenShiftClusterAdminCredential{
		ExpirationTimestamp: metadataapihelpers.PtrOrNil(from.ExpirationTimestamp),
		Kubeconfig:          metadataapihelpers.PtrOrNil(from.Kubeconfig),
	}
}

func (v version) MarshalClusterAdminCredential(from *coreapi.ClusterAdminCredential) ([]byte, error) {
	return coreapi.MarshalJSON(newClusterAdminCredential(from))
}

func (v version) UnmarshalClusterAdminCredentialRequest(_ []byte) (*coreapi.ClusterAdminCredentialRequest, error) {
	return &coreapi.ClusterAdminCredentialRequest{}, nil
}
