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

package client

//go:generate $MOCKGEN -typed -source=public_ip_addresses_client.go -destination=mock_public_ip_addresses_client.go -package client PublicIPAddressesClient

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

type PublicIPAddressesClient interface {
	NewListPager(resourceGroupName string, options *armnetwork.PublicIPAddressesClientListOptions) *runtime.Pager[armnetwork.PublicIPAddressesClientListResponse]
}

var _ PublicIPAddressesClient = (*armnetwork.PublicIPAddressesClient)(nil)
