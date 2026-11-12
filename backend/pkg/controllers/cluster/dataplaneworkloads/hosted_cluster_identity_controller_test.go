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
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/roleassignment"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/denyassignments"
	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	kubeapplierlistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	hcIdentitySubscriptionID    = "00000000-0000-0000-0000-000000000000"
	hcIdentityResourceGroupName = "test-rg"
	hcIdentityClusterName       = "test-cluster"
	hcIdentityFileCSIOperator   = string(azure.ClusterOperatorIdentifierFileCSIDriver)
	hcIdentityDiskCSIOperator   = string(azure.ClusterOperatorIdentifierDiskCSIDriver)
	hcIdentityImageRegistryOp   = string(azure.ClusterOperatorIdentifierImageRegistry)
	hcIdentityHCName            = "test-hc"
	hcIdentityHCNamespace       = "ocm-arohcppers-abc123"
)

var hcIdentityMgmtClusterResourceID = metadataapi.Must(azcorearm.ParseResourceID(
	"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))

var hcIdentityOtherMgmtClusterResourceID = metadataapi.Must(azcorearm.ParseResourceID(
	"/providers/microsoft.redhatopenshift/stamps/2/managementclusters/default"))

func testIdentityResourceID(name string) *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + hcIdentitySubscriptionID +
			"/resourceGroups/" + hcIdentityResourceGroupName +
			"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/" + name,
	))
}
func buildReadDesireWithHC(t *testing.T, hcName, hcNamespace string) *kubeapplierapi.ReadDesire {
	t.Helper()

	hcJSON, err := json.Marshal(map[string]interface{}{
		"apiVersion": "hypershift.openshift.io/v1beta1",
		"kind":       "HostedCluster",
		"metadata":   map[string]interface{}{"name": hcName, "namespace": hcNamespace},
		"spec": map[string]interface{}{
			"platform": map[string]interface{}{
				"azure": map[string]interface{}{
					"azureAuthenticationConfig": map[string]interface{}{
						"azureAuthenticationConfigType": "ManagedIdentities",
						"managedIdentities": map[string]interface{}{
							"dataPlane": map[string]interface{}{
								"imageRegistryMSIClientID": "client-img",
								"diskMSIClientID":          "client-disk",
								"fileMSIClientID":          "client-file",
							},
						},
					},
				},
			},
		},
	})
	require.NoError(t, err, "failed to marshal minimal HostedCluster JSON")

	ridStr := kubeapplierapihelpers.ToClusterScopedReadDesireResourceIDString(
		hcIdentitySubscriptionID, hcIdentityResourceGroupName, hcIdentityClusterName,
		kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster,
	)
	rid, err := azcorearm.ParseResourceID(ridStr)
	require.NoError(t, err, "failed to parse ReadDesire resource ID")

	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   rid,
			PartitionKey: strings.ToLower(hcIdentityMgmtClusterResourceID.String()),
		},
		Spec: kubeapplierapi.ReadDesireSpec{ManagementCluster: hcIdentityMgmtClusterResourceID},
		Status: kubeapplierapi.ReadDesireStatus{
			Conditions:  []metav1.Condition{{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionTrue}},
			KubeContent: &k8sruntime.RawExtension{Raw: hcJSON},
		},
	}
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
		name      string
		mutate    func(*coreapi.Cluster, *coreapi.ServiceProviderCluster, coreapi.DataplaneOIDCFederationAssignmentKey)
		wantError bool
	}{
		{name: "completed configuration for current instance"},
		{name: "missing assignment", wantError: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			delete(s.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation, k)
		}},
		{name: "nil assignment", wantError: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			s.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[k] = nil
		}},
		{name: "new or outstanding configuration has no ensured stamp", wantError: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			s.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[k].EnsuredIdentity = nil
		}},
		{name: "deconfiguration blocks cutover", wantError: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			s.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[k].DeconfigureTimestamp = ptr.To(metav1.Now())
		}},
		{name: "old ensured instance", wantError: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			s.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[k].EnsuredIdentity.ClientID = "old"
		}},
		{name: "ARM client changed", wantError: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			s.Status.ManagedIdentityDetails[k.IdentityResourceID].MetadataFromARMUserAssignedIdentitiesAPI.ClientID = ptr.To("new")
		}},
		{name: "ARM principal changed", wantError: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			s.Status.ManagedIdentityDetails[k.IdentityResourceID].MetadataFromARMUserAssignedIdentitiesAPI.PrincipalID = ptr.To("new")
		}},
		{name: "ARM tenant changed", wantError: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			s.Status.ManagedIdentityDetails[k.IdentityResourceID].MetadataFromARMUserAssignedIdentitiesAPI.TenantID = ptr.To("new")
		}},
		{name: "missing ARM metadata", wantError: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			delete(s.Status.ManagedIdentityDetails, k.IdentityResourceID)
		}},
		{name: "unresolved ARM identity", wantError: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			s.Status.ManagedIdentityDetails[k.IdentityResourceID].MetadataFromARMUserAssignedIdentitiesAPI.ClientID = nil
		}},
		{name: "no configured identity", wantError: true, mutate: func(c *coreapi.Cluster, _ *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
			delete(c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators, k.OperatorName)
		}},
		{name: "shared identity requires its own operator confirmation", wantError: true, mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, k coreapi.DataplaneOIDCFederationAssignmentKey) {
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
			syncer := &hostedClusterDataPlaneIdentitySyncer{}
			id, principal, err := syncer.resolveAndGateOperatorClientID(cluster, spc, azure.ClusterOperatorIdentifierImageRegistry)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "client-img", id)
			assert.Equal(t, "p1", principal)
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

func newIdentityIntentSyncer(
	t *testing.T, ctx context.Context,
	cluster *coreapi.Cluster, spc *coreapi.ServiceProviderCluster,
	observed *kubeapplierapi.ReadDesire,
) (*hostedClusterDataPlaneIdentitySyncer, *corecosmosstoragetesting.MockResourcesDBClient) {
	t.Helper()
	db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
	require.NoError(t, err)
	reads := []*kubeapplierapi.ReadDesire{}
	if observed != nil {
		reads = append(reads, observed)
	}
	return &hostedClusterDataPlaneIdentitySyncer{
		resourcesDBClient: db,
		readDesireLister:  &kubeapplierlistertesting.SliceReadDesireLister{Desires: reads},
		enabled:           true,
	}, db
}

func readIdentitySPC(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) *coreapi.ServiceProviderCluster {
	t.Helper()
	spc, err := db.ServiceProviderClusters(hcIdentitySubscriptionID, hcIdentityResourceGroupName, hcIdentityClusterName).
		Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	return spc
}

type conflictingSPCClient struct {
	corecosmosstorage.ResourcesDBClient
	beforeReplace func(context.Context) error
}

func (c *conflictingSPCClient) ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster] {
	return &conflictingSPCCRUD{
		ResourceCRUD: c.ResourcesDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName),
		client:       c,
	}
}

type conflictingSPCCRUD struct {
	cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster]
	client *conflictingSPCClient
}

func (c *conflictingSPCCRUD) Replace(ctx context.Context, spc *coreapi.ServiceProviderCluster, options *azcosmos.ItemOptions) (*coreapi.ServiceProviderCluster, error) {
	if c.client.beforeReplace != nil {
		write := c.client.beforeReplace
		c.client.beforeReplace = nil
		if err := write(ctx); err != nil {
			return nil, err
		}
	}
	return c.ResourceCRUD.Replace(ctx, spc, options)
}

func TestIdentityIntentRetriesConcurrentCertificateWrite(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	cluster, spc := newFullyResolvedClusterAndSPC(t)
	syncer, db := newIdentityIntentSyncer(t, ctx, cluster, spc,
		buildReadDesireWithHC(t, hcIdentityHCName, hcIdentityHCNamespace))
	client := &conflictingSPCClient{ResourcesDBClient: db}
	client.beforeReplace = func(ctx context.Context) error {
		crud := db.ServiceProviderClusters(hcIdentitySubscriptionID, hcIdentityResourceGroupName, hcIdentityClusterName)
		current, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		if err != nil {
			return err
		}
		changed := current.DeepCopy()
		changed.Spec.DesiredHostedCluster = &v1beta1.HostedCluster{
			Spec: v1beta1.HostedClusterSpec{Configuration: &v1beta1.ClusterConfiguration{APIServer: &configv1.APIServerSpec{ServingCerts: configv1.APIServerServingCerts{NamedCertificates: []configv1.APIServerNamedServingCert{{Names: []string{"api.example.com"}, ServingCertificate: configv1.SecretNameReference{Name: "serving-cert"}}}}}}},
		}
		_, err = crud.Replace(ctx, changed, nil)
		return err
	}
	syncer.resourcesDBClient = client
	require.NoError(t, syncer.SyncOnce(ctx, testKey()))
	got := readIdentitySPC(t, ctx, db)
	assert.True(t, got.Spec.HostedClusterDataPlaneIdentitiesOwnedByRP)
	assert.True(t, clientIDsFromHostedCluster(got.Spec.DesiredHostedCluster).Complete())
	assert.Equal(t, "serving-cert", got.Spec.DesiredHostedCluster.Spec.Configuration.APIServer.ServingCerts.NamedCertificates[0].ServingCertificate.Name)
}

func TestIdentityIntentRetriesWithFreshClusterMapping(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	cluster, spc := newFullyResolvedClusterAndSPC(t)
	cluster.CustomerProperties.Platform.ManagedResourceGroup = "managed-rg"
	imageID := testIdentityResourceID("image-registry-mi")
	imageKey := strings.ToLower(imageID.String())
	spc.Status.DataPlaneOperatorsManagedIdentities.Identities = map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
		imageKey: {ResourceID: imageID, ClientID: ptr.To("client-img"), PrincipalID: ptr.To("p1")},
	}
	roleID := metadataapi.Must(azcorearm.ParseResourceID(
		"/providers/Microsoft.Authorization/roleDefinitions/11111111-1111-1111-1111-111111111111"))
	denyID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + hcIdentitySubscriptionID + "/resourceGroups/managed-rg/providers/Microsoft.Authorization/denyAssignments/22222222-2222-2222-2222-222222222222"))
	for _, assignmentType := range denyassignments.DataPlaneOperatorDenyAssignmentTypes(cluster, hcIdentityImageRegistryOp) {
		spc.Status.AzureResources.DenyAssignments.AzureResources = append(
			spc.Status.AzureResources.DenyAssignments.AzureResources,
			coreapi.DenyAssignmentReference{DenyAssignmentType: assignmentType, DenyAssignmentResourceID: denyID, ExcludedPrincipalIDs: []string{"p1"}},
		)
	}
	scope, err := coreapihelpers.ToResourceGroupResourceID(hcIdentitySubscriptionID, "managed-rg")
	require.NoError(t, err)
	assignmentID := roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(scope.String(), "p1", roleID.String())
	spc.Status.AzureResources.RoleAssignments.AzureResources = []*azcorearm.ResourceID{metadataapi.Must(azcorearm.ParseResourceID(assignmentID))}
	spc.Spec.HostedClusterDataPlaneIdentitiesOwnedByRP = true
	spc.Spec.DesiredHostedCluster = &v1beta1.HostedCluster{}
	require.NoError(t, kubeapplierhelpers.SetDesiredHostedClusterDataPlaneClientIDs(
		spc.Spec.DesiredHostedCluster,
		hostedClusterDataPlaneClientIDs{ImageRegistryMSIClientID: "client-img-new", DiskMSIClientID: "client-disk", FileMSIClientID: "client-file"},
	))
	syncer, db := newIdentityIntentSyncer(t, ctx, cluster, spc, nil)
	syncer.clusterScopedIdentitiesConfig = &azure.ClusterScopedIdentitiesConfig{
		DataPlaneOperatorsIdentities: azure.DataPlaneOperatorsIdentities{
			azure.ClusterOperatorIdentifierImageRegistry: {
				KubernetesServiceAccounts: []*azure.KubernetesServiceAccount{{Name: "image-registry", Namespace: "openshift-image-registry"}},
				BaseClusterScopedOperatorIdentity: azure.BaseClusterScopedOperatorIdentity{
					BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
						RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{{ResourceID: roleID}},
					},
				},
			},
		},
	}
	client := &conflictingSPCClient{ResourcesDBClient: db}
	client.beforeReplace = func(ctx context.Context) error {
		clusterCRUD := db.HCPClusters(hcIdentitySubscriptionID, hcIdentityResourceGroupName)
		currentCluster, err := clusterCRUD.Get(ctx, hcIdentityClusterName)
		if err != nil {
			return err
		}
		changedCluster := currentCluster.DeepCopy()
		changedCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators[hcIdentityImageRegistryOp] = testIdentityResourceID("replacement-image-registry-mi")
		if _, err := clusterCRUD.Replace(ctx, changedCluster, nil); err != nil {
			return err
		}
		spcCRUD := db.ServiceProviderClusters(hcIdentitySubscriptionID, hcIdentityResourceGroupName, hcIdentityClusterName)
		currentSPC, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		if err != nil {
			return err
		}
		changedSPC := currentSPC.DeepCopy()
		changedSPC.Spec.DesiredHostedCluster.Annotations = map[string]string{"concurrent": "write"}
		_, err = spcCRUD.Replace(ctx, changedSPC, nil)
		return err
	}
	syncer.resourcesDBClient = client
	require.NoError(t, syncer.SyncOnce(ctx, testKey()))
	got := readIdentitySPC(t, ctx, db)
	assert.Equal(t, "client-img-new", clientIDsFromHostedCluster(got.Spec.DesiredHostedCluster).ImageRegistryMSIClientID)
	assert.Equal(t, "write", got.Spec.DesiredHostedCluster.Annotations["concurrent"])
}

func TestIdentityIntentSeedsObservedValuesAtomically(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	cluster, spc := newFullyResolvedClusterAndSPC(t)
	spc.Spec.DesiredHostedCluster = &v1beta1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"other-controller": "keep"}},
	}
	syncer, db := newIdentityIntentSyncer(t, ctx, cluster, spc,
		buildReadDesireWithHC(t, hcIdentityHCName, hcIdentityHCNamespace))
	require.NoError(t, syncer.SyncOnce(ctx, testKey()))
	got := readIdentitySPC(t, ctx, db)
	assert.True(t, got.Spec.HostedClusterDataPlaneIdentitiesOwnedByRP)
	assert.Equal(t, hostedClusterDataPlaneClientIDs{
		ImageRegistryMSIClientID: "client-img",
		DiskMSIClientID:          "client-disk",
		FileMSIClientID:          "client-file",
	}, clientIDsFromHostedCluster(got.Spec.DesiredHostedCluster))
	assert.Equal(t, "keep", got.Spec.DesiredHostedCluster.Annotations["other-controller"])
	version := got.InstanceVersion
	require.NoError(t, syncer.SyncOnce(ctx, testKey()))
	assert.Equal(t, version, readIdentitySPC(t, ctx, db).InstanceVersion)
}

func TestIdentityIntentSelectsCurrentManagementClusterObservation(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	cluster, spc := newFullyResolvedClusterAndSPC(t)
	stale := buildReadDesireWithHC(t, hcIdentityHCName, hcIdentityHCNamespace)
	stale.Spec.ManagementCluster = hcIdentityOtherMgmtClusterResourceID
	stale.PartitionKey = strings.ToLower(hcIdentityOtherMgmtClusterResourceID.String())
	stale.Status.KubeContent.Raw = []byte(strings.ReplaceAll(string(stale.Status.KubeContent.Raw), `"client-img"`, `"stale-client"`))
	current := buildReadDesireWithHC(t, hcIdentityHCName, hcIdentityHCNamespace)
	syncer, db := newIdentityIntentSyncer(t, ctx, cluster, spc, nil)
	syncer.readDesireLister = &kubeapplierlistertesting.SliceReadDesireLister{Desires: []*kubeapplierapi.ReadDesire{stale, current}}
	require.NoError(t, syncer.SyncOnce(ctx, testKey()))
	got := readIdentitySPC(t, ctx, db)
	assert.True(t, got.Spec.HostedClusterDataPlaneIdentitiesOwnedByRP)
	assert.Equal(t, "client-img", clientIDsFromHostedCluster(got.Spec.DesiredHostedCluster).ImageRegistryMSIClientID)
}

func TestIdentityIntentWaitsForObservedHostedCluster(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	cluster, spc := newFullyResolvedClusterAndSPC(t)
	syncer, db := newIdentityIntentSyncer(t, ctx, cluster, spc, nil)
	require.NoError(t, syncer.SyncOnce(ctx, testKey()))
	assert.False(t, readIdentitySPC(t, ctx, db).Spec.HostedClusterDataPlaneIdentitiesOwnedByRP)
}

func TestIdentityIntentFailsClosedAfterTakeover(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	cluster, spc := newFullyResolvedClusterAndSPC(t)
	spc.Spec.HostedClusterDataPlaneIdentitiesOwnedByRP = true
	spc.Spec.DesiredHostedCluster = &v1beta1.HostedCluster{}
	syncer, db := newIdentityIntentSyncer(t, ctx, cluster, spc, nil)
	require.ErrorContains(t, syncer.SyncOnce(ctx, testKey()), "incomplete")
	got := readIdentitySPC(t, ctx, db)
	assert.True(t, got.Spec.HostedClusterDataPlaneIdentitiesOwnedByRP)
	assert.False(t, clientIDsFromHostedCluster(got.Spec.DesiredHostedCluster).Complete())
}

func TestIdentityIntentDisabledDoesNotUndoTakeover(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	cluster, spc := newFullyResolvedClusterAndSPC(t)
	spc.Spec.HostedClusterDataPlaneIdentitiesOwnedByRP = true
	syncer, db := newIdentityIntentSyncer(t, ctx, cluster, spc, nil)
	syncer.enabled = false
	require.NoError(t, syncer.SyncOnce(ctx, testKey()))
	assert.True(t, readIdentitySPC(t, ctx, db).Spec.HostedClusterDataPlaneIdentitiesOwnedByRP)
}

func TestResolveReadyClientIDsRequiresCurrentAzureAccessPerOperator(t *testing.T) {
	t.Parallel()
	cluster, spc := newFullyResolvedClusterAndSPC(t)
	cluster.CustomerProperties.Platform.ManagedResourceGroup = "managed-rg"
	imageID := testIdentityResourceID("image-registry-mi")
	imageKey := strings.ToLower(imageID.String())
	roleID := metadataapi.Must(azcorearm.ParseResourceID(
		"/providers/Microsoft.Authorization/roleDefinitions/11111111-1111-1111-1111-111111111111"))
	config := &azure.ClusterScopedIdentitiesConfig{
		DataPlaneOperatorsIdentities: azure.DataPlaneOperatorsIdentities{
			azure.ClusterOperatorIdentifierImageRegistry: {
				KubernetesServiceAccounts: []*azure.KubernetesServiceAccount{{Name: "image-registry", Namespace: "openshift-image-registry"}},
				BaseClusterScopedOperatorIdentity: azure.BaseClusterScopedOperatorIdentity{
					BaseClusterScopedIdentity: azure.BaseClusterScopedIdentity{
						RoleDefinitions: []*azure.ClusterScopedIdentityRoleDefinition{{ResourceID: roleID}},
					},
				},
			},
		},
	}
	syncer := &hostedClusterDataPlaneIdentitySyncer{clusterScopedIdentitiesConfig: config}
	current := hostedClusterDataPlaneClientIDs{
		ImageRegistryMSIClientID: "old-image-client",
		DiskMSIClientID:          "old-disk-client",
		FileMSIClientID:          "old-file-client",
	}
	check := func(expectedImage string) {
		t.Helper()
		got := syncer.resolveReadyClientIDs(testCtx(t), cluster, spc, current)
		assert.Equal(t, expectedImage, got.ImageRegistryMSIClientID)
		assert.Equal(t, current.DiskMSIClientID, got.DiskMSIClientID)
		assert.Equal(t, current.FileMSIClientID, got.FileMSIClientID)
	}

	check(current.ImageRegistryMSIClientID)
	spc.Status.DataPlaneOperatorsManagedIdentities.Identities = map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
		imageKey: {ResourceID: imageID, ClientID: ptr.To("client-img"), PrincipalID: ptr.To("p1")},
	}
	denyID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + hcIdentitySubscriptionID + "/resourceGroups/managed-rg/providers/Microsoft.Authorization/denyAssignments/22222222-2222-2222-2222-222222222222"))
	for _, assignmentType := range denyassignments.DataPlaneOperatorDenyAssignmentTypes(cluster, hcIdentityImageRegistryOp) {
		spc.Status.AzureResources.DenyAssignments.AzureResources = append(
			spc.Status.AzureResources.DenyAssignments.AzureResources,
			coreapi.DenyAssignmentReference{DenyAssignmentType: assignmentType, DenyAssignmentResourceID: denyID, ExcludedPrincipalIDs: []string{"p1"}},
		)
	}
	check(current.ImageRegistryMSIClientID) // role still absent
	scope, err := coreapihelpers.ToResourceGroupResourceID(hcIdentitySubscriptionID, "managed-rg")
	require.NoError(t, err)
	assignmentID := roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(scope.String(), "p1", roleID.String())
	spc.Status.AzureResources.RoleAssignments.AzureResources = []*azcorearm.ResourceID{metadataapi.Must(azcorearm.ParseResourceID(assignmentID))}
	check("client-img")
	spc.Status.AzureResources.DenyAssignments.AzureResources[0].ExcludedPrincipalIDs = nil
	check(current.ImageRegistryMSIClientID) // a missing exclusion blocks cutover
}

func TestIdentityIntentBootstrapGuards(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		mutate    func(*coreapi.Cluster, *coreapi.ServiceProviderCluster, *kubeapplierapi.ReadDesire)
		wantError bool
	}{
		{name: "cluster deleting", mutate: func(c *coreapi.Cluster, _ *coreapi.ServiceProviderCluster, _ *kubeapplierapi.ReadDesire) {
			c.ServiceProviderProperties.DeletionTimestamp = ptr.To(metav1.Now())
		}},
		{name: "cluster not created in CS", mutate: func(c *coreapi.Cluster, _ *coreapi.ServiceProviderCluster, _ *kubeapplierapi.ReadDesire) {
			c.ServiceProviderProperties.ClusterServiceID = nil
		}},
		{name: "no placement", mutate: func(_ *coreapi.Cluster, s *coreapi.ServiceProviderCluster, _ *kubeapplierapi.ReadDesire) {
			s.Status.ManagementClusterResourceID = nil
		}},
		{name: "observation from another management cluster", mutate: func(_ *coreapi.Cluster, _ *coreapi.ServiceProviderCluster, r *kubeapplierapi.ReadDesire) {
			r.Spec.ManagementCluster = hcIdentityOtherMgmtClusterResourceID
		}},
		{name: "unsuccessful observation", mutate: func(_ *coreapi.Cluster, _ *coreapi.ServiceProviderCluster, r *kubeapplierapi.ReadDesire) {
			r.Status.Conditions = nil
		}},
		{name: "invalid observation", wantError: true, mutate: func(_ *coreapi.Cluster, _ *coreapi.ServiceProviderCluster, r *kubeapplierapi.ReadDesire) {
			r.Status.KubeContent.Raw = []byte(`{`)
		}},
		{name: "incomplete observation", wantError: true, mutate: func(_ *coreapi.Cluster, _ *coreapi.ServiceProviderCluster, r *kubeapplierapi.ReadDesire) {
			r.Status.KubeContent.Raw = []byte(strings.ReplaceAll(string(r.Status.KubeContent.Raw), `"fileMSIClientID":"client-file"`, `"fileMSIClientID":""`))
		}},
		{name: "wrong authentication type", mutate: func(_ *coreapi.Cluster, _ *coreapi.ServiceProviderCluster, r *kubeapplierapi.ReadDesire) {
			r.Status.KubeContent.Raw = []byte(strings.ReplaceAll(string(r.Status.KubeContent.Raw), `"azureAuthenticationConfigType":"ManagedIdentities"`, `"azureAuthenticationConfigType":"WorkloadIdentities"`))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := testCtx(t)
			cluster, spc := newFullyResolvedClusterAndSPC(t)
			observed := buildReadDesireWithHC(t, hcIdentityHCName, hcIdentityHCNamespace)
			tc.mutate(cluster, spc, observed)
			syncer, db := newIdentityIntentSyncer(t, ctx, cluster, spc, observed)
			before := readIdentitySPC(t, ctx, db)
			err := syncer.SyncOnce(ctx, testKey())
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			after := readIdentitySPC(t, ctx, db)
			assert.False(t, after.Spec.HostedClusterDataPlaneIdentitiesOwnedByRP)
			assert.Equal(t, before.InstanceVersion, after.InstanceVersion)
		})
	}
}
