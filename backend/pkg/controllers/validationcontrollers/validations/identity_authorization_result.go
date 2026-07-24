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

package validations

import (
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azurecheckaccessv2client "github.com/Azure/checkaccess-v2-go-sdk/client"
)

// checkaccessv2AuthorizationDecisionData captures a single CheckAccessV2 decision where the identity was not granted access.
type checkaccessv2AuthorizationDecisionData struct {
	ActionID       string                                  `json:"actionId"`
	IsDataAction   bool                                    `json:"isDataAction"`
	AccessDecision azurecheckaccessv2client.AccessDecision `json:"accessDecision"`
}

// identityResourceMissingPermissions records the set of actions that an identity was denied or not granted on a specific Azure resource.
// It is only instantiated when at least one action is missing; Decisions is always non-empty and contains only NotAllowed or Denied entries.
type identityResourceMissingPermissions struct {
	Resource  *azcorearm.ResourceID                     `json:"resource"`
	Identity  *azcorearm.ResourceID                     `json:"identity"`
	Decisions []*checkaccessv2AuthorizationDecisionData `json:"decisions"`
}
