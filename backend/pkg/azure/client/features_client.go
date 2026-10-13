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

//go:generate $MOCKGEN -typed -source=features_client.go -destination=mock_features_client.go -package client FeaturesClient

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armfeatures"
)

// FeaturesClient is an interface that defines the methods that we want to use from the
// Client type in the Azure Go SDK
// (https://github.com/Azure/azure-sdk-for-go/tree/main/sdk/resourcemanager/resources/armfeatures).
// The aim is to only contain methods that are defined in the Azure Go SDK armfeatures Client.
// If you need to use a method provided by the Azure Go SDK armfeatures Client but it is not
// defined in this interface then it has to be added here and all the types implementing this
// interface have to implement the new method.
type FeaturesClient interface {
	Get(ctx context.Context, resourceProviderNamespace string, featureName string, options *armfeatures.ClientGetOptions) (armfeatures.ClientGetResponse, error)
}

// interface guard to ensure that all methods defined in the FeaturesClient
// interface are implemented by the real Azure Go SDK armfeatures Client.
var _ FeaturesClient = (*armfeatures.Client)(nil)
