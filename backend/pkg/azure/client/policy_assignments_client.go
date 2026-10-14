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

//go:generate $MOCKGEN -typed -source=policy_assignments_client.go -destination=mock_policy_assignments_client.go -package client PolicyAssignmentsClient,PolicyDefinitionsClient

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armpolicy"
)

// PolicyAssignmentsClient is an interface that defines the methods that
// we want to use from the AssignmentsClient type in the Azure Go SDK
// (https://github.com/Azure/azure-sdk-for-go/tree/main/sdk/resourcemanager/resources/armpolicy).
// The aim is to only contain methods that are defined in the Azure Go SDK
// AssignmentsClient client.
// If you need to use a method provided by the Azure Go SDK AssignmentsClient
// client but it is not defined in this interface then it has to be added here and all
// the types implementing this interface have to implement the new method.
type PolicyAssignmentsClient interface {
	// NewListPager lists all policy assignments effective within the client's
	// configured subscription, including those inherited from parent management
	// groups and those applied to objects contained within the subscription.
	NewListPager(
		options *armpolicy.AssignmentsClientListOptions,
	) *runtime.Pager[armpolicy.AssignmentsClientListResponse]
}

// interface guard to ensure that all methods defined in the PolicyAssignmentsClient
// interface are implemented by the real Azure Go SDK AssignmentsClient
// client. This interface guard should always compile
var _ PolicyAssignmentsClient = (*armpolicy.AssignmentsClient)(nil)

// PolicyDefinitionsClient is an interface that defines the methods that
// we want to use from the DefinitionsClient type in the Azure Go SDK
// (https://github.com/Azure/azure-sdk-for-go/tree/main/sdk/resourcemanager/resources/armpolicy).
// The aim is to only contain methods that are defined in the Azure Go SDK
// DefinitionsClient client.
// If you need to use a method provided by the Azure Go SDK DefinitionsClient
// client but it is not defined in this interface then it has to be added here and all
// the types implementing this interface have to implement the new method.
type PolicyDefinitionsClient interface {
	// Get retrieves a custom policy definition in the client's configured subscription.
	Get(ctx context.Context, policyDefinitionName string, options *armpolicy.DefinitionsClientGetOptions) (
		armpolicy.DefinitionsClientGetResponse, error)
	// GetBuiltIn retrieves a built-in policy definition.
	GetBuiltIn(ctx context.Context, policyDefinitionName string, options *armpolicy.DefinitionsClientGetBuiltInOptions) (
		armpolicy.DefinitionsClientGetBuiltInResponse, error)
}

// interface guard to ensure that all methods defined in the PolicyDefinitionsClient
// interface are implemented by the real Azure Go SDK DefinitionsClient
// client. This interface guard should always compile
var _ PolicyDefinitionsClient = (*armpolicy.DefinitionsClient)(nil)
