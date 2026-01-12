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

import (
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/internal/fpa"
)

// FirstPartyApplicationClientBuilderType is a type that represents the type of the FPAClientBuilder
// interface. It is used to ensure that that interface is incompatible
// with other client builder interfaces that might have the same set of
// methods
type FirstPartyApplicationClientBuilderType string

const (
	// FirstPartyApplicationClientBuilderTypeValue is the value of the FPABuilderType type that
	// represents the FPA client builder.
	FirstPartyApplicationClientBuilderTypeValue FirstPartyApplicationClientBuilderType = "FPA"
)

type FirstPartyApplicationClientBuilder interface {
	// BuilderType returns the type of the client builder. Its only
	// purpose is to ensure that this interface is incompatible
	// with other client builder interfaces that might have the same
	// set of methods. In that way we ensure that they cannot be used
	// interchangeably.
	BuilderType() FirstPartyApplicationClientBuilderType
	ResourceProvidersClient(tenantID string, subscriptionID string) (ResourceProvidersClient, error)
}

type firstPartyApplicationClientBuilder struct {
	fpaTokenCredRetriever fpa.FirstPartyApplicationTokenCredentialRetriever
	options               *azcorearm.ClientOptions
}

var _ FirstPartyApplicationClientBuilder = (*firstPartyApplicationClientBuilder)(nil)

// NewFirstPartyApplicationClientBuilder instantiates a FPAClientBuilder. When clients are instantiated with it the FPA token credential
// retriever is leveraged to get a FPA Token Credential, and the provided ARM client options.
func NewFirstPartyApplicationClientBuilder(
	tokenCredRetriever fpa.FirstPartyApplicationTokenCredentialRetriever, options *azcorearm.ClientOptions,
) FirstPartyApplicationClientBuilder {
	return &firstPartyApplicationClientBuilder{
		fpaTokenCredRetriever: tokenCredRetriever,
		options:               options,
	}
}

func (b *firstPartyApplicationClientBuilder) ResourceProvidersClient(tenantID string, subscriptionID string) (ResourceProvidersClient, error) {
	creds, err := b.fpaTokenCredRetriever.RetrieveCredential(tenantID)
	if err != nil {
		return nil, err
	}

	return armresources.NewProvidersClient(subscriptionID, creds, b.options)
}

func (b *firstPartyApplicationClientBuilder) BuilderType() FirstPartyApplicationClientBuilderType {
	return FirstPartyApplicationClientBuilderTypeValue
}
