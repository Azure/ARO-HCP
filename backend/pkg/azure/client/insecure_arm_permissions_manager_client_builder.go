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

//go:generate $MOCKGEN -typed -source=insecure_arm_permissions_manager_client_builder.go -destination=mock_insecure_arm_permissions_manager_client_builder.go -package client InsecureARMPermissionsManagerClientBuilder

import (
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"
)

// InsecureARMPermissionsManagerClientBuilderType represents the type of the
// InsecureARMPermissionsManagerClientBuilder interface. Its only purpose is to ensure
// that this interface is incompatible with other client builder interfaces that
// might have the same set of methods, so they cannot be used interchangeably.
type InsecureARMPermissionsManagerClientBuilderType string

const (
	// InsecureARMPermissionsManagerClientBuilderTypeValue is the value of the
	// InsecureARMPermissionsManagerClientBuilderType that represents the ARM permissions
	// manager client builder.
	InsecureARMPermissionsManagerClientBuilderTypeValue InsecureARMPermissionsManagerClientBuilderType = "InsecureARMPermissionsManager"
)

// InsecureARMPermissionsManagerClientBuilder builds Azure clients authenticated as the
// insecure ARM permissions manager ("ARM Helper") identity. It is only available
// when the backend runs without a real FPA (HasRealFPA is false). The identity
// authenticates in its own fixed tenant, so unlike the FPA builder these methods
// do not take a tenant ID.
type InsecureARMPermissionsManagerClientBuilder interface {
	// BuilderType returns the type of the client builder. Its only purpose is to
	// ensure this interface is incompatible with other client builder interfaces
	// that might have the same set of methods.
	BuilderType() InsecureARMPermissionsManagerClientBuilderType
	DeploymentStacksClient(subscriptionID string) (*armdeploymentstacks.Client, error)
}

type insecureARMPermissionsManagerClientBuilder struct {
	tokenCredRetriever InsecureARMPermissionsManagerIdentityTokenCredentialRetriever
	options            *azcorearm.ClientOptions
}

var _ InsecureARMPermissionsManagerClientBuilder = (*insecureARMPermissionsManagerClientBuilder)(nil)

// NewInsecureARMPermissionsManagerClientBuilder instantiates an
// InsecureARMPermissionsManagerClientBuilder. When clients are instantiated with it the
// ARM permissions manager token credential retriever is leveraged to obtain a
// token credential, together with the provided ARM client options.
func NewInsecureARMPermissionsManagerClientBuilder(
	tokenCredRetriever InsecureARMPermissionsManagerIdentityTokenCredentialRetriever, options *azcorearm.ClientOptions,
) InsecureARMPermissionsManagerClientBuilder {
	return &insecureARMPermissionsManagerClientBuilder{
		tokenCredRetriever: tokenCredRetriever,
		options:            options,
	}
}

func (b *insecureARMPermissionsManagerClientBuilder) BuilderType() InsecureARMPermissionsManagerClientBuilderType {
	return InsecureARMPermissionsManagerClientBuilderTypeValue
}

func (b *insecureARMPermissionsManagerClientBuilder) DeploymentStacksClient(subscriptionID string) (*armdeploymentstacks.Client, error) {
	creds, err := b.tokenCredRetriever.RetrieveCredential()
	if err != nil {
		return nil, err
	}
	return armdeploymentstacks.NewClient(subscriptionID, creds, b.options)
}
