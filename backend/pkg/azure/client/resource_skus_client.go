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

//go:generate $MOCKGEN -typed -source=resource_skus_client.go -destination=mock_resource_skus_client.go -package client ResourceSKUsClient

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
)

// ResourceSKUsClient is an interface that defines the methods that we want to use from the
// ResourceSKUsClient type in the Azure Go SDK
// (https://github.com/Azure/azure-sdk-for-go/tree/main/sdk/resourcemanager/compute/armcompute).
// The aim is to only contain methods that are defined in the Azure Go SDK ResourceSKUsClient.
// If you need to use a method provided by the Azure Go SDK ResourceSKUsClient but it is not
// defined in this interface then it has to be added here and all the types implementing this
// interface have to implement the new method.
type ResourceSKUsClient interface {
	NewListPager(options *armcompute.ResourceSKUsClientListOptions) *runtime.Pager[armcompute.ResourceSKUsClientListResponse]
}

// interface guard to ensure that all methods defined in the ResourceSKUsClient
// interface are implemented by the real Azure Go SDK ResourceSKUsClient.
var _ ResourceSKUsClient = (*armcompute.ResourceSKUsClient)(nil)
