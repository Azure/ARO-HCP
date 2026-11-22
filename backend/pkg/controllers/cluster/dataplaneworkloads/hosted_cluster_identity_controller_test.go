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

package dataplaneworkloads

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	hcIdentitySubscriptionID    = "00000000-0000-0000-0000-000000000000"
	hcIdentityResourceGroupName = "test-rg"
	hcIdentityClusterName       = "test-cluster"
	hcIdentityFileCSIOperator   = string(azure.ClusterOperatorIdentifierFileCSIDriver)
	hcIdentityDiskCSIOperator   = string(azure.ClusterOperatorIdentifierDiskCSIDriver)
	hcIdentityImageRegistryOp   = string(azure.ClusterOperatorIdentifierImageRegistry)
)

var hcIdentityMgmtClusterResourceID = metadataapi.Must(azcorearm.ParseResourceID(
	"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))

func testIdentityResourceID(name string) *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + hcIdentitySubscriptionID +
			"/resourceGroups/" + hcIdentityResourceGroupName +
			"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/" + name,
	))
}
func testCtx(t *testing.T) context.Context {
	t.Helper()
	return utils.ContextWithLogger(context.Background(), testr.New(t))
}

func newFullyResolvedClusterAndSPC(t *testing.T) (*coreapi.Cluster, *coreapi.ServiceProviderCluster) {
	t.Helper()
	rid := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + hcIdentitySubscriptionID + "/resourceGroups/" + hcIdentityResourceGroupName + "/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + hcIdentityClusterName))
	cluster := &coreapi.Cluster{
		CosmosMetadata:  coreapi.CosmosMetadata{ResourceID: rid, PartitionKey: strings.ToLower(rid.SubscriptionID)},
		TrackedResource: coreapi.TrackedResource{Resource: coreapi.Resource{ID: rid, Name: hcIdentityClusterName, Type: rid.ResourceType.String()}},
	}
	csID := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/cs-cluster-abc"))
	cluster.ServiceProviderProperties.ClusterServiceID = &csID
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators = map[string]*azcorearm.ResourceID{}
	spcID := metadataapi.Must(azcorearm.ParseResourceID(rid.String() + "/serviceProviderClusters/" + coreapi.ServiceProviderClusterResourceName))
	spc := &coreapi.ServiceProviderCluster{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spcID, PartitionKey: strings.ToLower(rid.SubscriptionID)}}
	spc.Status.ManagementClusterResourceID = hcIdentityMgmtClusterResourceID
	spc.Status.ManagedIdentityDetails = map[string]*coreapi.ManagedIdentityMetadata{}
	spc.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus{}
	for _, op := range []struct{ operator, name, client, principal string }{
		{hcIdentityImageRegistryOp, "image-registry-mi", "client-img", "p1"},
		{hcIdentityDiskCSIOperator, "disk-mi", "client-disk", "p2"},
		{hcIdentityFileCSIOperator, "file-mi", "client-file", "p3"},
	} {
		id := testIdentityResourceID(op.name)
		key := strings.ToLower(id.String())
		cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators[op.operator] = id
		spc.Status.ManagedIdentityDetails[key] = &coreapi.ManagedIdentityMetadata{ResourceID: id, MetadataFromARMUserAssignedIdentitiesAPI: &coreapi.IdentityMetadataValue{ClientID: ptr.To(op.client), PrincipalID: ptr.To(op.principal), TenantID: ptr.To("tenant")}}
		target := coreapi.DataplaneOIDCFederationIdentityInstance{ClientID: op.client, PrincipalID: op.principal, TenantID: "tenant"}
		spc.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[coreapi.DataplaneOIDCFederationAssignmentKey{IdentityResourceID: key, OperatorName: op.operator}] = &coreapi.DataplaneOIDCFederationAssignmentStatus{TargetIdentity: target, EnsuredIdentity: ptr.To(target)}
	}
	return cluster, spc
}

// EnsuredIdentity is historical completed-configuration evidence. The consumer
// gates on the current ARM tuple and operator assignment, not Azure recheck health.
func TestHostedClusterIdentityFederationContract(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		mutate      func(*coreapi.Cluster, *coreapi.ServiceProviderCluster, coreapi.DataplaneOIDCFederationAssignmentKey)
		wantPending bool
	}{
		{name: "completed configuration for current instance"},
		{name: "missing assignment", wantPending: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			delete(s.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation, k)
		}},
		{name: "nil assignment", wantPending: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			s.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[k] = nil
		}},
		{name: "new or outstanding configuration has no ensured stamp", wantPending: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			s.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[k].EnsuredIdentity = nil
		}},
		{name: "deconfiguration blocks cutover", wantPending: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			s.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[k].DeconfigureTimestamp = ptr.To(metav1.Now())
		}},
		{name: "old ensured instance", wantPending: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			s.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[k].EnsuredIdentity.ClientID = "old"
		}},
		{name: "ARM client changed", wantPending: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			s.Status.ManagedIdentityDetails[k.IdentityResourceID].MetadataFromARMUserAssignedIdentitiesAPI.ClientID = ptr.To("new")
		}},
		{name: "ARM principal changed", wantPending: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			s.Status.ManagedIdentityDetails[k.IdentityResourceID].MetadataFromARMUserAssignedIdentitiesAPI.PrincipalID = ptr.To("new")
		}},
		{name: "ARM tenant changed", wantPending: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			s.Status.ManagedIdentityDetails[k.IdentityResourceID].MetadataFromARMUserAssignedIdentitiesAPI.TenantID = ptr.To("new")
		}},
		{name: "missing ARM metadata", wantPending: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			delete(s.Status.ManagedIdentityDetails, k.IdentityResourceID)
		}},
		{name: "unresolved ARM identity", wantPending: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			s.Status.ManagedIdentityDetails[k.IdentityResourceID].MetadataFromARMUserAssignedIdentitiesAPI.ClientID = nil
		}},
		{name: "no configured identity", wantPending: true, mutate: func(c *coreapi.Cluster, _ *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			delete(c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators, k.OperatorName)
		}},
		{name: "shared identity requires its own operator confirmation", wantPending: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			entry := s.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[k]
			delete(s.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation, k)
			k.OperatorName = hcIdentityDiskCSIOperator
			s.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[k] = entry
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cluster, spc := newFullyResolvedClusterAndSPC(t)
			key := coreapi.DataplaneOIDCFederationAssignmentKey{IdentityResourceID: strings.ToLower(testIdentityResourceID("image-registry-mi").String()), OperatorName: hcIdentityImageRegistryOp}
			if tc.mutate != nil {
				tc.mutate(cluster, spc, key)
			}
			id := ensuredOperatorClientID(cluster, spc, azure.ClusterOperatorIdentifierImageRegistry)
			if tc.wantPending {
				require.Empty(t, id)
				return
			}
			assert.Equal(t, "client-img", id)
		})
	}
}

func testKey() controllerutils.HCPClusterKey {
	return controllerutils.HCPClusterKey{
		SubscriptionID:    hcIdentitySubscriptionID,
		ResourceGroupName: hcIdentityResourceGroupName,
		HCPClusterName:    hcIdentityClusterName,
	}
}

func TestIdentityIntentConcurrentSPCUpdate(t *testing.T) {
	ctx := testCtx(t)
	cluster, spc := newFullyResolvedClusterAndSPC(t)
	db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
	require.NoError(t, err)
	key := testKey()
	crud := db.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	cached, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	concurrent := cached.DeepCopy()
	concurrent.Spec.DesiredHostedCluster = &v1beta1.HostedCluster{Spec: v1beta1.HostedClusterSpec{OperatorConfiguration: &v1beta1.OperatorConfiguration{IngressOperator: &v1beta1.IngressOperatorSpec{}}}}
	concurrent.Spec.DesiredHostedCluster.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate.Name = "serving-cert"
	_, err = crud.Replace(ctx, concurrent, nil)
	require.NoError(t, err)
	syncer := &hostedClusterDataPlaneIdentitySyncer{resourcesDBClient: db,
		clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}},
		serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{cached}},
	}
	require.NoError(t, syncer.SyncOnce(ctx, key))
	beforeRefresh, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.Equal(t, concurrent.Spec, beforeRefresh.Spec)
	require.Nil(t, cached.Spec.DesiredHostedCluster)
	syncer.serviceProviderClusterLister = &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: db}
	require.NoError(t, syncer.SyncOnce(ctx, key))
	saved, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	hc := saved.Spec.DesiredHostedCluster
	require.Equal(t, "serving-cert", hc.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate.Name)
	require.Equal(t, v1beta1.DataPlaneManagedIdentities{ImageRegistryMSIClientID: "client-img", DiskMSIClientID: "client-disk", FileMSIClientID: "client-file"}, hc.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.DataPlane)
	require.NoError(t, syncer.SyncOnce(ctx, key))
	// One newly resolved operator cannot publish until the others are ready.
	disk := strings.ToLower(testIdentityResourceID("disk-mi").String())
	saved.Status.ManagedIdentityDetails[disk].MetadataFromARMUserAssignedIdentitiesAPI.ClientID = ptr.To("new-disk")
	assignment := saved.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[coreapi.DataplaneOIDCFederationAssignmentKey{IdentityResourceID: disk, OperatorName: hcIdentityDiskCSIOperator}]
	assignment.TargetIdentity.ClientID = "new-disk"
	assignment.EnsuredIdentity = ptr.To(assignment.TargetIdentity)
	delete(saved.Status.ManagedIdentityDetails, strings.ToLower(testIdentityResourceID("file-mi").String()))
	pending, err := crud.Replace(ctx, saved, nil)
	require.NoError(t, err)
	require.NoError(t, syncer.SyncOnce(ctx, key))
	after, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.Equal(t, pending.InstanceVersion, after.InstanceVersion)
	require.Equal(t, "client-disk", after.Spec.DesiredHostedCluster.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.DataPlane.DiskMSIClientID)
}
