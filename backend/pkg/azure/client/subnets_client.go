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

//go:generate $MOCKGEN -typed -source=subnets_client.go -destination=mock_subnets_client.go -package client SubnetsClient

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

// SubnetsClient is an interface that defines the methods that we want to use
// from the SubnetsClient type in the Azure Go SDK
// (https://github.com/Azure/azure-sdk-for-go/tree/main/sdk/resourcemanager/network/armnetwork).
// The aim is to only contain methods that are defined in the Azure Go SDK
// SubnetsClient client.
// If you need to use a method provided by the Azure Go SDK SubnetsClient
// client but it is not defined in this interface then it has to be added here and all
// the types implementing this interface have to implement the new method.
type SubnetsClient interface {
	Get(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error)
}

// interface guard to ensure that all methods defined in the SubnetsClient
// interface are implemented by the real Azure Go SDK SubnetsClient
// client. This interface guard should always compile
var _ SubnetsClient = (*armnetwork.SubnetsClient)(nil)
