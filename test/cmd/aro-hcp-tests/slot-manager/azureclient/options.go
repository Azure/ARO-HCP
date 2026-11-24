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

// Package azureclient holds the Azure SDK client options shared by every
// slot-manager client.
package azureclient

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/internal/azsdk"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/azclient"
)

// ClientOptions returns the options every slot-manager Azure SDK client must be
// created with, the same ones the E2E framework applies to its own clients: the
// telemetry application ID, and the retry of the transient poll responses that
// ARM returns while a deployment becomes visible. The retry policy only acts on
// ARM deployment requests and passes everything else through.
func ClientOptions() azcore.ClientOptions {
	options := azsdk.NewClientOptions(azsdk.ComponentE2E)
	options.PerCallPolicies = []policy.Policy{azclient.NewLROPollerRetryPolicy(nil)}
	return options
}

// ARMClientOptions is ClientOptions for ARM clients.
func ARMClientOptions() *azcorearm.ClientOptions {
	return &azcorearm.ClientOptions{ClientOptions: ClientOptions()}
}
