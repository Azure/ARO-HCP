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

package azuremockclient

import (
	"context"
	"fmt"

	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
)

// DenyAssignmentsClientFunc adapts a function to the DenyAssignmentsClient.Get interface.
// Tests set GetFunc to control the response per call.
type DenyAssignmentsClientFunc struct {
	GetFunc func(ctx context.Context, scope string, denyAssignmentID string, options *armauthorization.DenyAssignmentsClientGetOptions) (armauthorization.DenyAssignmentsClientGetResponse, error)
}

var _ azureclient.DenyAssignmentsClient = (*DenyAssignmentsClientFunc)(nil)

func (m *DenyAssignmentsClientFunc) Get(ctx context.Context, scope string, denyAssignmentID string, options *armauthorization.DenyAssignmentsClientGetOptions) (armauthorization.DenyAssignmentsClientGetResponse, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, scope, denyAssignmentID, options)
	}
	return armauthorization.DenyAssignmentsClientGetResponse{}, fmt.Errorf("GetFunc not set")
}

func (m *DenyAssignmentsClientFunc) NewListForResourceGroupPager(_ string, _ *armauthorization.DenyAssignmentsClientListForResourceGroupOptions) *azruntime.Pager[armauthorization.DenyAssignmentsClientListForResourceGroupResponse] {
	return nil
}

// RoleAssignmentsClientFunc adapts functions to the RoleAssignmentsClient interface.
// Tests set the function fields to control the response per call.
type RoleAssignmentsClientFunc struct {
	GetByIDFunc    func(ctx context.Context, roleAssignmentID string, options *armauthorization.RoleAssignmentsClientGetByIDOptions) (armauthorization.RoleAssignmentsClientGetByIDResponse, error)
	CreateFunc     func(ctx context.Context, scope, roleAssignmentName string, parameters armauthorization.RoleAssignmentCreateParameters, options *armauthorization.RoleAssignmentsClientCreateOptions) (armauthorization.RoleAssignmentsClientCreateResponse, error)
	DeleteByIDFunc func(ctx context.Context, roleAssignmentID string, options *armauthorization.RoleAssignmentsClientDeleteByIDOptions) (armauthorization.RoleAssignmentsClientDeleteByIDResponse, error)

	GetByIDCalls    []string
	CreateCalls     []RoleAssignmentCreateCall
	DeleteByIDCalls []string
}

type RoleAssignmentCreateCall struct {
	Scope              string
	RoleAssignmentName string
	Parameters         armauthorization.RoleAssignmentCreateParameters
}

var _ azureclient.RoleAssignmentsClient = (*RoleAssignmentsClientFunc)(nil)

func (m *RoleAssignmentsClientFunc) GetByID(ctx context.Context, roleAssignmentID string, options *armauthorization.RoleAssignmentsClientGetByIDOptions) (armauthorization.RoleAssignmentsClientGetByIDResponse, error) {
	m.GetByIDCalls = append(m.GetByIDCalls, roleAssignmentID)
	if m.GetByIDFunc != nil {
		return m.GetByIDFunc(ctx, roleAssignmentID, options)
	}
	return armauthorization.RoleAssignmentsClientGetByIDResponse{}, fmt.Errorf("GetByIDFunc not set")
}

func (m *RoleAssignmentsClientFunc) Create(ctx context.Context, scope string, roleAssignmentName string, parameters armauthorization.RoleAssignmentCreateParameters, options *armauthorization.RoleAssignmentsClientCreateOptions) (armauthorization.RoleAssignmentsClientCreateResponse, error) {
	m.CreateCalls = append(m.CreateCalls, RoleAssignmentCreateCall{
		Scope:              scope,
		RoleAssignmentName: roleAssignmentName,
		Parameters:         parameters,
	})
	if m.CreateFunc != nil {
		return m.CreateFunc(ctx, scope, roleAssignmentName, parameters, options)
	}
	return armauthorization.RoleAssignmentsClientCreateResponse{}, fmt.Errorf("CreateFunc not set")
}

func (m *RoleAssignmentsClientFunc) DeleteByID(ctx context.Context, roleAssignmentID string, options *armauthorization.RoleAssignmentsClientDeleteByIDOptions) (armauthorization.RoleAssignmentsClientDeleteByIDResponse, error) {
	m.DeleteByIDCalls = append(m.DeleteByIDCalls, roleAssignmentID)
	if m.DeleteByIDFunc != nil {
		return m.DeleteByIDFunc(ctx, roleAssignmentID, options)
	}
	return armauthorization.RoleAssignmentsClientDeleteByIDResponse{}, fmt.Errorf("DeleteByIDFunc not set")
}

// GenericResourcesClientFunc adapts functions to the GenericResourcesClient interface.
// Tests set the function fields to control the response.
// BeginCreateOrUpdateByID and BeginDeleteByID return a nil *Poller and an error — to simulate
// success, return (nil, nil) and the calling code will call PollUntilDone on nil.
// To avoid that, the tests should exercise paths that don't reach PollUntilDone (e.g. error paths)
// or the mock should capture the call without returning a real poller.
//
// For paths that call PollUntilDone, set CreateErr/DeleteErr to non-nil to prevent the nil-pointer dereference.
type GenericResourcesClientFunc struct {
	CreateCalls []GenericResourceCreateCall
	DeleteCalls []string
	CreateErr   error
	DeleteErr   error
}

type GenericResourceCreateCall struct {
	ResourceID string
	APIVersion string
	Resource   armresources.GenericResource
}

var _ azureclient.GenericResourcesClient = (*GenericResourcesClientFunc)(nil)

func (m *GenericResourcesClientFunc) BeginCreateOrUpdateByID(ctx context.Context, resourceID string, apiVersion string, parameters armresources.GenericResource, options *armresources.ClientBeginCreateOrUpdateByIDOptions) (*azruntime.Poller[armresources.ClientCreateOrUpdateByIDResponse], error) {
	m.CreateCalls = append(m.CreateCalls, GenericResourceCreateCall{
		ResourceID: resourceID,
		APIVersion: apiVersion,
		Resource:   parameters,
	})
	if m.CreateErr != nil {
		return nil, m.CreateErr
	}
	return nil, fmt.Errorf("GenericResourcesClientFunc: set CreateErr to control this path; PollUntilDone cannot be called on a nil poller")
}

func (m *GenericResourcesClientFunc) BeginDeleteByID(ctx context.Context, resourceID string, apiVersion string, options *armresources.ClientBeginDeleteByIDOptions) (*azruntime.Poller[armresources.ClientDeleteByIDResponse], error) {
	m.DeleteCalls = append(m.DeleteCalls, resourceID)
	if m.DeleteErr != nil {
		return nil, m.DeleteErr
	}
	return nil, fmt.Errorf("GenericResourcesClientFunc: set DeleteErr to control this path; PollUntilDone cannot be called on a nil poller")
}

// FirstPartyApplicationClientBuilderFunc builds mock Azure clients.
type FirstPartyApplicationClientBuilderFunc struct {
	GenericResourcesClientVal azureclient.GenericResourcesClient
	GenericResourcesClientErr error
	DenyAssignmentsClientVal  azureclient.DenyAssignmentsClient
	DenyAssignmentsClientErr  error
	RoleAssignmentsClientVal  azureclient.RoleAssignmentsClient
	RoleAssignmentsClientErr  error
}

var _ azureclient.FirstPartyApplicationClientBuilder = (*FirstPartyApplicationClientBuilderFunc)(nil)

func (m *FirstPartyApplicationClientBuilderFunc) BuilderType() azureclient.FirstPartyApplicationClientBuilderType {
	return azureclient.FirstPartyApplicationClientBuilderTypeValue
}

func (m *FirstPartyApplicationClientBuilderFunc) ResourceGroupsClient(tenantID string, subscriptionID string) (azureclient.ResourceGroupsClient, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *FirstPartyApplicationClientBuilderFunc) ResourceProvidersClient(tenantID string, subscriptionID string) (azureclient.ResourceProvidersClient, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *FirstPartyApplicationClientBuilderFunc) ResourceSKUsClient(tenantID string, subscriptionID string) (azureclient.ResourceSKUsClient, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *FirstPartyApplicationClientBuilderFunc) UsageClient(tenantID string, subscriptionID string) (azureclient.UsageClient, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *FirstPartyApplicationClientBuilderFunc) GenericResourcesClient(tenantID string, subscriptionID string) (azureclient.GenericResourcesClient, error) {
	return m.GenericResourcesClientVal, m.GenericResourcesClientErr
}

func (m *FirstPartyApplicationClientBuilderFunc) DenyAssignmentsClient(tenantID string, subscriptionID string) (azureclient.DenyAssignmentsClient, error) {
	return m.DenyAssignmentsClientVal, m.DenyAssignmentsClientErr
}

func (m *FirstPartyApplicationClientBuilderFunc) RoleAssignmentsClient(tenantID string, subscriptionID string) (azureclient.RoleAssignmentsClient, error) {
	if m.RoleAssignmentsClientErr != nil {
		return nil, m.RoleAssignmentsClientErr
	}
	if m.RoleAssignmentsClientVal != nil {
		return m.RoleAssignmentsClientVal, nil
	}
	return nil, fmt.Errorf("not implemented")
}
