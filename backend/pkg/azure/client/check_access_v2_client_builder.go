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
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	checkaccessv2 "github.com/Azure/checkaccess-v2-go-sdk/client"

	"github.com/Azure/ARO-HCP/internal/fpa"
)

// CheckAccessV2ClientBuilder allows to build a CheckAccessV2Client instance to
// interact with Microsoft's Check Access V2 API.
type CheckAccessV2ClientBuilder interface {
	// Build builds a CheckAccessV2Client instance to interact with Microsoft's Check Access V2 API.
	// tenantID is the Azure Tenant ID where the Azure identities for which we want to check permissions reside.
	Build(tenantID string) (CheckAccessV2Client, error)
}

// realFPAIdentityCheckAccessV2ClientBuilder builds a CheckAccessV2Client instance to
// interact with Microsoft's Check Access V2 API using a real FPA identity.
type realFPAIdentityCheckAccessV2ClientBuilder struct {
	fpaIdentityTokenCredentialRetriever fpa.FirstPartyApplicationTokenCredentialRetriever
	checkAccessV2Endpoint               string
	checkAccessV2Scope                  string
	options                             *azcore.ClientOptions
}

// NewRealFPAIdentityCheckAccessV2ClientBuilder instantiates a CheckAccessV2ClientBuilder that will use a
// real FPA identity to build a CheckAccessV2Client instance.
func NewRealFPAIdentityCheckAccessV2ClientBuilder(fpaIdentityTokenCredentialRetriever fpa.FirstPartyApplicationTokenCredentialRetriever, checkAccessV2Endpoint string, checkAccessV2Scope string, options *azcore.ClientOptions) CheckAccessV2ClientBuilder {
	return &realFPAIdentityCheckAccessV2ClientBuilder{
		fpaIdentityTokenCredentialRetriever: fpaIdentityTokenCredentialRetriever,
		checkAccessV2Scope:                  checkAccessV2Scope,
		checkAccessV2Endpoint:               checkAccessV2Endpoint,
		options:                             options,
	}
}

func (b *realFPAIdentityCheckAccessV2ClientBuilder) Build(tenantID string) (CheckAccessV2Client, error) {
	creds, err := b.fpaIdentityTokenCredentialRetriever.RetrieveCredential(tenantID)
	if err != nil {
		return nil, err
	}
	return checkaccessv2.NewRemotePDPClient(b.checkAccessV2Endpoint, b.checkAccessV2Scope, creds, b.options)
}

// armHelperIdentityCheckAccessV2ClientBuilder builds a CheckAccessV2Client instance to
// interact with Microsoft's Check Access V2 API using the ARM Helper identity.
// armHelperIdentityCheckAccessV2ClientBuilder only supports instantiating CheckAccessV2Client instances
// with the same tenant ID as the one of the ARM Helper identity.
type armHelperIdentityCheckAccessV2ClientBuilder struct {
	armHelperIdentityTokenCredentialRetriever ARMHelperIdentityTokenCredentialRetriever
	checkAccessV2Endpoint                     string
	checkAccessV2Scope                        string
	options                                   *azcore.ClientOptions
}

// NewArmHelperIdentityCheckAccessV2ClientBuilder instantiates a CheckAccessV2ClientBuilder that will use a
// ARM Helper identity to build a CheckAccessV2Client instance. The returned CheckAccessV2ClientBuilder only
// supports instantiating CheckAccessV2Client instances with the same tenant ID as the one of the ARM Helper identity.
func NewArmHelperIdentityCheckAccessV2ClientBuilder(armHelperIdentityTokenCredentialRetriever ARMHelperIdentityTokenCredentialRetriever, checkAccessV2Endpoint string, checkAccessV2Scope string, options *azcore.ClientOptions) CheckAccessV2ClientBuilder {
	return &armHelperIdentityCheckAccessV2ClientBuilder{
		armHelperIdentityTokenCredentialRetriever: armHelperIdentityTokenCredentialRetriever,
		checkAccessV2Scope:                        checkAccessV2Scope,
		checkAccessV2Endpoint:                     checkAccessV2Endpoint,
		options:                                   options,
	}
}

// Build builds a CheckAccessV2Client instance to interact with Microsoft's Check Access V2 API using the ARM Helper identity.
// The tenant ID must be the same as where the ARM Helper identity is created.
func (b *armHelperIdentityCheckAccessV2ClientBuilder) Build(tenantID string) (CheckAccessV2Client, error) {
	creds, err := b.armHelperIdentityTokenCredentialRetriever.RetrieveCredential()
	if err != nil {
		return nil, err
	}
	return checkaccessv2.NewRemotePDPClient(b.checkAccessV2Endpoint, b.checkAccessV2Scope, creds, b.options)
}
