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

package corelistertesting

import (
	"context"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
)

// SliceClusterLister implements corelisters.ClusterLister backed by a slice.
type SliceClusterLister struct {
	Clusters []*coreapi.HCPOpenShiftCluster
}

var _ corelisters.ClusterLister = &SliceClusterLister{}

func (l *SliceClusterLister) List(ctx context.Context) ([]*coreapi.HCPOpenShiftCluster, error) {
	return l.Clusters, nil
}

func (l *SliceClusterLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*coreapi.HCPOpenShiftCluster, error) {
	for _, c := range l.Clusters {
		if c.ID == nil {
			continue
		}
		if strings.EqualFold(c.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(c.ID.ResourceGroupName, resourceGroupName) &&
			strings.EqualFold(c.ID.Name, clusterName) {
			return c, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *SliceClusterLister) ListForResourceGroup(ctx context.Context, subscriptionID, resourceGroupName string) ([]*coreapi.HCPOpenShiftCluster, error) {
	var result []*coreapi.HCPOpenShiftCluster
	for _, c := range l.Clusters {
		if c.ID == nil {
			continue
		}
		if strings.EqualFold(c.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(c.ID.ResourceGroupName, resourceGroupName) {
			result = append(result, c)
		}
	}
	return result, nil
}

// SliceNodePoolLister implements corelisters.NodePoolLister backed by a slice.
type SliceNodePoolLister struct {
	NodePools []*coreapi.HCPOpenShiftClusterNodePool
}

var _ corelisters.NodePoolLister = &SliceNodePoolLister{}

func (l *SliceNodePoolLister) List(ctx context.Context) ([]*coreapi.HCPOpenShiftClusterNodePool, error) {
	return l.NodePools, nil
}

func (l *SliceNodePoolLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) (*coreapi.HCPOpenShiftClusterNodePool, error) {
	for _, np := range l.NodePools {
		if np.ID == nil {
			continue
		}
		if strings.EqualFold(np.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(np.ID.ResourceGroupName, resourceGroupName) &&
			nodePoolMatchesCluster(np.ID, clusterName) &&
			strings.EqualFold(np.ID.Name, nodePoolName) {
			return np, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *SliceNodePoolLister) ListForResourceGroup(ctx context.Context, subscriptionID, resourceGroupName string) ([]*coreapi.HCPOpenShiftClusterNodePool, error) {
	var result []*coreapi.HCPOpenShiftClusterNodePool
	for _, np := range l.NodePools {
		if np.ID == nil {
			continue
		}
		if strings.EqualFold(np.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(np.ID.ResourceGroupName, resourceGroupName) {
			result = append(result, np)
		}
	}
	return result, nil
}

func (l *SliceNodePoolLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*coreapi.HCPOpenShiftClusterNodePool, error) {
	var result []*coreapi.HCPOpenShiftClusterNodePool
	for _, np := range l.NodePools {
		if np.ID == nil {
			continue
		}
		if strings.EqualFold(np.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(np.ID.ResourceGroupName, resourceGroupName) &&
			nodePoolMatchesCluster(np.ID, clusterName) {
			result = append(result, np)
		}
	}
	return result, nil
}

// SliceActiveOperationLister implements corelisters.ActiveOperationLister backed by a slice.
type SliceActiveOperationLister struct {
	Operations []*coreapi.Operation
}

var _ corelisters.ActiveOperationLister = &SliceActiveOperationLister{}

func (l *SliceActiveOperationLister) List(ctx context.Context) ([]*coreapi.Operation, error) {
	return l.Operations, nil
}

func (l *SliceActiveOperationLister) Get(ctx context.Context, subscriptionID, name string) (*coreapi.Operation, error) {
	for _, op := range l.Operations {
		if op.OperationID == nil {
			continue
		}
		if strings.EqualFold(op.OperationID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(op.OperationID.Name, name) {
			return op, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

// ListActiveOperationsForCluster returns active operations for the cluster and its
// child resources (node pools, external auths), matching production lister semantics.
func (l *SliceActiveOperationLister) ListActiveOperationsForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*coreapi.Operation, error) {
	clusterKey := coreapi.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	return l.listByPrefix(clusterKey), nil
}

func (l *SliceActiveOperationLister) ListActiveOperationsForNodePool(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) ([]*coreapi.Operation, error) {
	nodePoolKey := coreapi.ToNodePoolResourceIDString(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	return l.listByPrefix(nodePoolKey), nil
}

func (l *SliceActiveOperationLister) ListActiveOperationsForExternalAuth(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) ([]*coreapi.Operation, error) {
	externalAuthKey := coreapi.ToExternalAuthResourceIDString(subscriptionID, resourceGroupName, clusterName, externalAuthName)
	return l.listByPrefix(externalAuthKey), nil
}

func (l *SliceActiveOperationLister) listByPrefix(prefix string) []*coreapi.Operation {
	var result []*coreapi.Operation
	for _, op := range l.Operations {
		if op.ExternalID == nil {
			continue
		}
		if strings.HasPrefix(strings.ToLower(op.ExternalID.String()), strings.ToLower(prefix)) {
			result = append(result, op)
		}
	}
	return result
}

// SliceExternalAuthLister implements corelisters.ExternalAuthLister backed by a slice.
type SliceExternalAuthLister struct {
	ExternalAuths []*coreapi.HCPOpenShiftClusterExternalAuth
}

var _ corelisters.ExternalAuthLister = &SliceExternalAuthLister{}

func (l *SliceExternalAuthLister) List(ctx context.Context) ([]*coreapi.HCPOpenShiftClusterExternalAuth, error) {
	return l.ExternalAuths, nil
}

func (l *SliceExternalAuthLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) (*coreapi.HCPOpenShiftClusterExternalAuth, error) {
	for _, ea := range l.ExternalAuths {
		if ea.ID == nil {
			continue
		}
		if strings.EqualFold(ea.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(ea.ID.ResourceGroupName, resourceGroupName) &&
			externalAuthMatchesCluster(ea.ID, clusterName) &&
			strings.EqualFold(ea.ID.Name, externalAuthName) {
			return ea, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *SliceExternalAuthLister) ListForResourceGroup(ctx context.Context, subscriptionID, resourceGroupName string) ([]*coreapi.HCPOpenShiftClusterExternalAuth, error) {
	var result []*coreapi.HCPOpenShiftClusterExternalAuth
	for _, ea := range l.ExternalAuths {
		if ea.ID == nil {
			continue
		}
		if strings.EqualFold(ea.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(ea.ID.ResourceGroupName, resourceGroupName) {
			result = append(result, ea)
		}
	}
	return result, nil
}

func (l *SliceExternalAuthLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*coreapi.HCPOpenShiftClusterExternalAuth, error) {
	var result []*coreapi.HCPOpenShiftClusterExternalAuth
	for _, ea := range l.ExternalAuths {
		if ea.ID == nil {
			continue
		}
		if strings.EqualFold(ea.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(ea.ID.ResourceGroupName, resourceGroupName) &&
			externalAuthMatchesCluster(ea.ID, clusterName) {
			result = append(result, ea)
		}
	}
	return result, nil
}

// SliceServiceProviderClusterLister implements corelisters.ServiceProviderClusterLister backed by a slice.
type SliceServiceProviderClusterLister struct {
	ServiceProviderClusters []*coreapi.ServiceProviderCluster
}

var _ corelisters.ServiceProviderClusterLister = &SliceServiceProviderClusterLister{}

func (l *SliceServiceProviderClusterLister) List(ctx context.Context) ([]*coreapi.ServiceProviderCluster, error) {
	return l.ServiceProviderClusters, nil
}

func (l *SliceServiceProviderClusterLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*coreapi.ServiceProviderCluster, error) {
	for _, spc := range l.ServiceProviderClusters {
		resourceID := spc.GetResourceID()
		if resourceID == nil {
			continue
		}
		if strings.EqualFold(resourceID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(resourceID.ResourceGroupName, resourceGroupName) &&
			serviceProviderClusterMatchesCluster(resourceID, clusterName) {
			return spc, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *SliceServiceProviderClusterLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*coreapi.ServiceProviderCluster, error) {
	var result []*coreapi.ServiceProviderCluster
	for _, spc := range l.ServiceProviderClusters {
		resourceID := spc.GetResourceID()
		if resourceID == nil {
			continue
		}
		if strings.EqualFold(resourceID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(resourceID.ResourceGroupName, resourceGroupName) &&
			serviceProviderClusterMatchesCluster(resourceID, clusterName) {
			result = append(result, spc)
		}
	}
	return result, nil
}

// SliceServiceProviderNodePoolLister implements corelisters.ServiceProviderNodePoolLister backed by a slice.
type SliceServiceProviderNodePoolLister struct {
	ServiceProviderNodePools []*coreapi.ServiceProviderNodePool
}

var _ corelisters.ServiceProviderNodePoolLister = &SliceServiceProviderNodePoolLister{}

func (l *SliceServiceProviderNodePoolLister) List(ctx context.Context) ([]*coreapi.ServiceProviderNodePool, error) {
	return l.ServiceProviderNodePools, nil
}

func (l *SliceServiceProviderNodePoolLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) (*coreapi.ServiceProviderNodePool, error) {
	for _, spnp := range l.ServiceProviderNodePools {
		resourceID := spnp.GetResourceID()
		if resourceID == nil {
			continue
		}
		if strings.EqualFold(resourceID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(resourceID.ResourceGroupName, resourceGroupName) &&
			serviceProviderNodePoolMatchesCluster(resourceID, clusterName) &&
			serviceProviderNodePoolMatchesNodePool(resourceID, nodePoolName) {
			return spnp, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *SliceServiceProviderNodePoolLister) ListForNodePool(ctx context.Context, subscriptionName, resourceGroupName, clusterName, nodePoolName string) ([]*coreapi.ServiceProviderNodePool, error) {
	var result []*coreapi.ServiceProviderNodePool
	for _, spnp := range l.ServiceProviderNodePools {
		resourceID := spnp.GetResourceID()
		if resourceID == nil {
			continue
		}
		if strings.EqualFold(resourceID.SubscriptionID, subscriptionName) &&
			strings.EqualFold(resourceID.ResourceGroupName, resourceGroupName) &&
			serviceProviderNodePoolMatchesCluster(resourceID, clusterName) &&
			serviceProviderNodePoolMatchesNodePool(resourceID, nodePoolName) {
			result = append(result, spnp)
		}
	}
	return result, nil
}

// SliceServiceProviderExternalAuthLister implements corelisters.ServiceProviderExternalAuthLister backed by a slice.
type SliceServiceProviderExternalAuthLister struct {
	ServiceProviderExternalAuths []*coreapi.ServiceProviderExternalAuth
}

var _ corelisters.ServiceProviderExternalAuthLister = &SliceServiceProviderExternalAuthLister{}

func (l *SliceServiceProviderExternalAuthLister) List(ctx context.Context) ([]*coreapi.ServiceProviderExternalAuth, error) {
	return l.ServiceProviderExternalAuths, nil
}

func (l *SliceServiceProviderExternalAuthLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) (*coreapi.ServiceProviderExternalAuth, error) {
	for _, spea := range l.ServiceProviderExternalAuths {
		resourceID := spea.GetResourceID()
		if resourceID == nil {
			continue
		}
		if strings.EqualFold(resourceID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(resourceID.ResourceGroupName, resourceGroupName) &&
			serviceProviderExternalAuthMatchesCluster(resourceID, clusterName) &&
			serviceProviderExternalAuthMatchesExternalAuth(resourceID, externalAuthName) {
			return spea, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *SliceServiceProviderExternalAuthLister) ListForExternalAuth(ctx context.Context, subscriptionName, resourceGroupName, clusterName, externalAuthName string) ([]*coreapi.ServiceProviderExternalAuth, error) {
	var result []*coreapi.ServiceProviderExternalAuth
	for _, spea := range l.ServiceProviderExternalAuths {
		resourceID := spea.GetResourceID()
		if resourceID == nil {
			continue
		}
		if strings.EqualFold(resourceID.SubscriptionID, subscriptionName) &&
			strings.EqualFold(resourceID.ResourceGroupName, resourceGroupName) &&
			serviceProviderExternalAuthMatchesCluster(resourceID, clusterName) &&
			serviceProviderExternalAuthMatchesExternalAuth(resourceID, externalAuthName) {
			result = append(result, spea)
		}
	}
	return result, nil
}

// SliceManagementClusterContentLister implements corelisters.ManagementClusterContentLister backed by a slice.
type SliceManagementClusterContentLister struct {
	Contents []*coreapi.ManagementClusterContent
}

var _ corelisters.ManagementClusterContentLister = &SliceManagementClusterContentLister{}

func (l *SliceManagementClusterContentLister) List(ctx context.Context) ([]*coreapi.ManagementClusterContent, error) {
	return l.Contents, nil
}

func (l *SliceManagementClusterContentLister) GetForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName, managementClusterContentName string) (*coreapi.ManagementClusterContent, error) {
	for _, c := range l.Contents {
		resourceID := c.GetResourceID()
		if resourceID == nil {
			continue
		}
		if strings.EqualFold(resourceID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(resourceID.ResourceGroupName, resourceGroupName) &&
			managementClusterContentMatchesCluster(resourceID, clusterName) &&
			strings.EqualFold(resourceID.Name, managementClusterContentName) {
			return c, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *SliceManagementClusterContentLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*coreapi.ManagementClusterContent, error) {
	var result []*coreapi.ManagementClusterContent
	for _, c := range l.Contents {
		resourceID := c.GetResourceID()
		if resourceID == nil {
			continue
		}
		if strings.EqualFold(resourceID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(resourceID.ResourceGroupName, resourceGroupName) &&
			managementClusterContentMatchesCluster(resourceID, clusterName) {
			result = append(result, c)
		}
	}
	return result, nil
}

func (l *SliceManagementClusterContentLister) ListForNodePool(ctx context.Context, subscriptionName, resourceGroupName, clusterName, nodePoolName string) ([]*coreapi.ManagementClusterContent, error) {
	prefix := coreapi.ToNodePoolResourceIDString(subscriptionName, resourceGroupName, clusterName, nodePoolName)
	var result []*coreapi.ManagementClusterContent
	for _, c := range l.Contents {
		resourceID := c.GetResourceID()
		if resourceID == nil {
			continue
		}
		if strings.HasPrefix(strings.ToLower(resourceID.String()), strings.ToLower(prefix)) {
			result = append(result, c)
		}
	}
	return result, nil
}

// SliceSubscriptionLister implements corelisters.SubscriptionLister backed by a slice.
type SliceSubscriptionLister struct {
	Subscriptions []*coreapi.Subscription
}

var _ corelisters.SubscriptionLister = &SliceSubscriptionLister{}

func (l *SliceSubscriptionLister) List(ctx context.Context) ([]*coreapi.Subscription, error) {
	return l.Subscriptions, nil
}

func (l *SliceSubscriptionLister) Get(ctx context.Context, subscriptionID string) (*coreapi.Subscription, error) {
	for _, s := range l.Subscriptions {
		resourceID := s.GetResourceID()
		if resourceID == nil {
			continue
		}
		if strings.EqualFold(resourceID.SubscriptionID, subscriptionID) {
			return s, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

// SliceBillingLister implements corelisters.BillingLister backed by a slice.
type SliceBillingLister struct {
	BillingDocuments []*billingcosmosstorage.BillingDocument
}

var _ corelisters.BillingLister = &SliceBillingLister{}

func (l *SliceBillingLister) List(ctx context.Context) ([]*billingcosmosstorage.BillingDocument, error) {
	return l.BillingDocuments, nil
}

func (l *SliceBillingLister) GetByID(ctx context.Context, billingDocID string) (*billingcosmosstorage.BillingDocument, error) {
	for _, bd := range l.BillingDocuments {
		if strings.EqualFold(bd.ID, billingDocID) {
			return bd, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *SliceBillingLister) ListForSubscription(ctx context.Context, subscriptionID string) ([]*billingcosmosstorage.BillingDocument, error) {
	var result []*billingcosmosstorage.BillingDocument
	for _, bd := range l.BillingDocuments {
		if strings.EqualFold(bd.SubscriptionID, subscriptionID) {
			result = append(result, bd)
		}
	}
	return result, nil
}

// SliceSystemAdminCredentialRequestLister implements listers.SystemAdminCredentialRequestLister backed by a slice.
type SliceSystemAdminCredentialRequestLister struct {
	CredentialRequests []*coreapi.SystemAdminCredentialRequest
}

var _ corelisters.SystemAdminCredentialRequestLister = &SliceSystemAdminCredentialRequestLister{}

func (l *SliceSystemAdminCredentialRequestLister) List(ctx context.Context) ([]*coreapi.SystemAdminCredentialRequest, error) {
	return l.CredentialRequests, nil
}

func (l *SliceSystemAdminCredentialRequestLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, credentialName string) (*coreapi.SystemAdminCredentialRequest, error) {
	for _, cred := range l.CredentialRequests {
		resourceID := cred.GetResourceID()
		if resourceID == nil {
			continue
		}
		if strings.EqualFold(resourceID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(resourceID.ResourceGroupName, resourceGroupName) &&
			resourceID.Parent != nil && strings.EqualFold(resourceID.Parent.Name, clusterName) &&
			strings.EqualFold(resourceID.Name, credentialName) {
			return cred, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *SliceSystemAdminCredentialRequestLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*coreapi.SystemAdminCredentialRequest, error) {
	var result []*coreapi.SystemAdminCredentialRequest
	for _, cred := range l.CredentialRequests {
		resourceID := cred.GetResourceID()
		if resourceID == nil {
			continue
		}
		if strings.EqualFold(resourceID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(resourceID.ResourceGroupName, resourceGroupName) &&
			resourceID.Parent != nil && strings.EqualFold(resourceID.Parent.Name, clusterName) {
			result = append(result, cred)
		}
	}
	return result, nil
}

// SliceSystemAdminCredentialRevocationLister implements listers.SystemAdminCredentialRevocationLister backed by a slice.
type SliceSystemAdminCredentialRevocationLister struct {
	CredentialRevocations []*coreapi.SystemAdminCredentialRevocation
}

var _ corelisters.SystemAdminCredentialRevocationLister = &SliceSystemAdminCredentialRevocationLister{}

func (l *SliceSystemAdminCredentialRevocationLister) List(ctx context.Context) ([]*coreapi.SystemAdminCredentialRevocation, error) {
	return l.CredentialRevocations, nil
}

func (l *SliceSystemAdminCredentialRevocationLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, revocationName string) (*coreapi.SystemAdminCredentialRevocation, error) {
	for _, rev := range l.CredentialRevocations {
		resourceID := rev.GetResourceID()
		if resourceID == nil {
			continue
		}
		if strings.EqualFold(resourceID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(resourceID.ResourceGroupName, resourceGroupName) &&
			resourceID.Parent != nil && strings.EqualFold(resourceID.Parent.Name, clusterName) &&
			strings.EqualFold(resourceID.Name, revocationName) {
			return rev, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}

func (l *SliceSystemAdminCredentialRevocationLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*coreapi.SystemAdminCredentialRevocation, error) {
	var result []*coreapi.SystemAdminCredentialRevocation
	for _, rev := range l.CredentialRevocations {
		resourceID := rev.GetResourceID()
		if resourceID == nil {
			continue
		}
		if strings.EqualFold(resourceID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(resourceID.ResourceGroupName, resourceGroupName) &&
			resourceID.Parent != nil && strings.EqualFold(resourceID.Parent.Name, clusterName) {
			result = append(result, rev)
		}
	}
	return result, nil
}

// SliceControllerLister implements corelisters.ControllerLister backed by a
// slice. The List-for-parent methods filter to controllers whose resource
// ID hangs DIRECTLY off the requested parent — i.e. they have exactly one
// path segment beyond the parent's resource ID. That excludes
// controllers nested further down (e.g. a node-pool controller is not
// returned by ListForCluster for that node pool's parent cluster).
type SliceControllerLister struct {
	Controllers []*coreapi.Controller
}

var _ corelisters.ControllerLister = &SliceControllerLister{}

func (l *SliceControllerLister) List(_ context.Context) ([]*coreapi.Controller, error) {
	return l.Controllers, nil
}

func (l *SliceControllerLister) ListForResourceGroup(_ context.Context, subscriptionID, resourceGroupName string) ([]*coreapi.Controller, error) {
	out := []*coreapi.Controller{}
	for _, c := range l.Controllers {
		if c.ResourceID == nil {
			continue
		}
		if strings.EqualFold(c.ResourceID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(c.ResourceID.ResourceGroupName, resourceGroupName) {
			out = append(out, c)
		}
	}
	return out, nil
}

func (l *SliceControllerLister) ListForCluster(_ context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*coreapi.Controller, error) {
	return listControllersUnderPrefix(l.Controllers,
		coreapi.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName))
}

func (l *SliceControllerLister) ListForNodePool(_ context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) ([]*coreapi.Controller, error) {
	return listControllersUnderPrefix(l.Controllers,
		coreapi.ToNodePoolResourceIDString(subscriptionID, resourceGroupName, clusterName, nodePoolName))
}

func (l *SliceControllerLister) ListForExternalAuth(_ context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) ([]*coreapi.Controller, error) {
	return listControllersUnderPrefix(l.Controllers,
		coreapi.ToExternalAuthResourceIDString(subscriptionID, resourceGroupName, clusterName, externalAuthName))
}

// listControllersUnderPrefix returns the controllers whose ResourceID is a
// direct child of parentResourceID — exactly one path segment-pair (type +
// name) below the parent.
func listControllersUnderPrefix(controllers []*coreapi.Controller, parentResourceID string) ([]*coreapi.Controller, error) {
	prefix := strings.ToLower(parentResourceID) + "/"
	out := []*coreapi.Controller{}
	for _, c := range controllers {
		if c.ResourceID == nil {
			continue
		}
		lowered := strings.ToLower(c.ResourceID.String())
		if !strings.HasPrefix(lowered, prefix) {
			continue
		}
		rest := strings.TrimPrefix(lowered, prefix)
		// A direct child has exactly one "/" in the trailing segment:
		// "<resourceType>/<name>".
		if strings.Count(rest, "/") != 1 {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}
