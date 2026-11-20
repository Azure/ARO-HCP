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

package clusterresources

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilsclock "k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/lru"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/metadataapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	fleetlistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	kubeapplierlistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testClusterServiceID  = "/api/clusters_mgmt/v1/clusters/abc123"
)

var testManagementClusterResourceID = metadataapi.Must(azcorearm.ParseResourceID(
	"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default",
))

func testKey() controllerutils.HCPClusterKey {
	return controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
}

func newCluster(opts ...func(*coreapi.Cluster)) *coreapi.Cluster {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))
	cluster := &coreapi.Cluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
			ClusterServiceID: metadataapihelpers.Ptr(metadataapi.Must(metadataapi.NewInternalID(testClusterServiceID))),
		},
	}
	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

func newSPC(mcResourceID *azcorearm.ResourceID) *coreapi.ServiceProviderCluster {
	spcResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/serviceProviderClusters/" + coreapi.ServiceProviderClusterResourceName,
	))
	return &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   spcResourceID,
			PartitionKey: strings.ToLower(spcResourceID.SubscriptionID),
		},
		Spec: coreapi.ServiceProviderClusterSpec{
			ControlPlaneVersion: coreapi.ServiceProviderClusterSpecVersion{
				DesiredVersion: func() *semver.Version { v := semver.MustParse("4.19.15"); return &v }(),
			},
			DesiredHostedCluster: &hsv1beta1.HostedCluster{
				Spec: hsv1beta1.HostedClusterSpec{
					Release: hsv1beta1.Release{Image: "quay.io/openshift-release-dev/ocp-release@sha256:testimage"},
				},
			},
		},
		Status: coreapi.ServiceProviderClusterStatus{
			ManagementClusterResourceID: mcResourceID,
		},
	}
}

func buildClusterResources(resources map[string]string) *arohcpv1alpha1.ClusterResources {
	cr, err := arohcpv1alpha1.NewClusterResources().
		Resources(resources).
		Build()
	if err != nil {
		panic(fmt.Sprintf("failed to build ClusterResources: %v", err))
	}
	return cr
}

func TestNeedsWork(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		cluster           *coreapi.Cluster
		managementCluster *azcorearm.ResourceID
		want              bool
	}{
		{
			name:              "returns true for cluster with ClusterServiceID and management cluster",
			cluster:           newCluster(),
			managementCluster: testManagementClusterResourceID,
			want:              true,
		},
		{
			name: "returns true for cluster being deleted with management cluster",
			cluster: newCluster(func(c *coreapi.Cluster) {
				now := metav1.Now()
				c.ServiceProviderProperties.DeletionTimestamp = &now
			}),
			managementCluster: testManagementClusterResourceID,
			want:              true,
		},
		{
			name: "returns false for cluster without ClusterServiceID",
			cluster: newCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.ClusterServiceID = nil
			}),
			managementCluster: testManagementClusterResourceID,
			want:              false,
		},
		{
			name:              "returns false when management cluster is nil",
			cluster:           newCluster(),
			managementCluster: nil,
			want:              false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := &clusterResourcesController{}
			got := c.NeedsWork(tt.cluster, tt.managementCluster)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLegacyIngressDesireSuppression(test *testing.T) {
	ctx := utils.ContextWithLogger(test.Context(), testr.New(test))
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	client := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	clients.Register(testManagementClusterResourceID, client)
	managementClusters := &fleetlistertesting.SliceManagementClusterLister{ManagementClusters: []*fleetapi.ManagementCluster{{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: testManagementClusterResourceID}}}}
	cluster := newCluster()
	serviceProviderCluster := newSPC(testManagementClusterResourceID)
	csClient := ocm.NewMockClusterServiceClientSpec(gomock.NewController(test))
	resources := buildClusterResources(map[string]string{
		"hosted":       `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"HostedCluster","metadata":{"name":"test-cluster","namespace":"hc"}}`,
		"managed":      `{"apiVersion":"cluster.open-cluster-management.io/v1","kind":"ManagedCluster","metadata":{"name":"test-cluster"}}`,
		"config":       `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"default-ingress","namespace":"open-cluster-management-policies"}}`,
		"sync":         `{"apiVersion":"secret-sync.x-k8s.io/v1alpha1","kind":"SecretSync","metadata":{"name":"default-ingress-tls-cert-test","namespace":"open-cluster-management-policies"}}`,
		"provider":     `{"apiVersion":"secrets-store.csi.x-k8s.io/v1","kind":"SecretProviderClass","metadata":{"name":"default-ingress-tls-cert-test","namespace":"open-cluster-management-policies"}}`,
		"kas":          `{"apiVersion":"secret-sync.x-k8s.io/v1alpha1","kind":"SecretSync","metadata":{"name":"kube-apiserver-tls-cert","namespace":"hc"}}`,
		"kas-provider": `{"apiVersion":"secrets-store.csi.x-k8s.io/v1","kind":"SecretProviderClass","metadata":{"name":"kube-apiserver-tls-cert","namespace":"hc"}}`,
		"signing":      `{"apiVersion":"secrets-store.csi.x-k8s.io/v1","kind":"SecretProviderClass","metadata":{"name":"signing-key-test","namespace":"hc"}}`,
	})
	csClient.EXPECT().GetClusterResources(gomock.Cond[context.Context](func(ctx context.Context) bool {
		deadline, ok := ctx.Deadline()
		remaining := time.Until(deadline)
		return ok && remaining > clusterResourcesFetchTimeout-time.Second && remaining <= clusterResourcesFetchTimeout
	}), gomock.Any()).Return(resources, nil)
	syncer := &clusterResourcesController{
		clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}},
		serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{serviceProviderCluster}},
		clustersServiceClient:        csClient, kubeApplierDBClients: clients,
		applyDesireLister: &kubeapplierlistertesting.DBApplyDesireLister{Clients: clients, Lister: managementClusters},
		readDesireLister:  &kubeapplierlistertesting.DBReadDesireLister{Clients: clients, Lister: managementClusters},
		resourcesCache:    lru.New(resourcesCacheCapacity),
		clock:             utilsclock.RealClock{},
	}
	require.NoError(test, syncer.SyncOnce(ctx, testKey()))
	desires, err := syncer.applyDesireLister.ListForCluster(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
	require.NoError(test, err)
	require.Len(test, desires, 2, "legacy ACM-based ingress desires should always be suppressed")
	desireNames := make([]string, 0, len(desires))
	for _, desire := range desires {
		desireNames = append(desireNames, desire.ResourceID.Name)
		require.Contains(test, []string{strings.ToLower("HostedCluster"), strings.ToLower("BoundServiceAccountSigningKeySecretProviderClass")}, desire.ResourceID.Name)
		if desire.ResourceID.Name == strings.ToLower("HostedCluster") {
			var hostedCluster unstructured.Unstructured
			require.NoError(test, json.Unmarshal(desire.Spec.ServerSideApply.KubeContent.Raw, &hostedCluster))
			name, found, err := unstructured.NestedString(hostedCluster.Object, "spec", "operatorConfiguration", "ingressOperator", "defaultCertificate", "name")
			require.NoError(test, err)
			require.True(test, found)
			require.Equal(test, controllerutils.ServiceProviderDefaultIngressWildcardServingCertName, name)
		}
	}
	for _, desireName := range []string{"ManagedCluster", "DefaultIngressConfigMap", "DefaultIngressWildcardCertSecretSync", "DefaultIngressWildcardCertSecretProviderClass"} {
		require.NotContains(test, desireNames, strings.ToLower(desireName))
	}
	require.NotContains(test, desireNames, strings.ToLower(DesireNameKubeAPIServerServingCertSecretSync))
	require.NotContains(test, desireNames, strings.ToLower(DesireNameKubeAPIServerServingCertSecretProviderClass))
	require.Contains(test, desireNames, strings.ToLower("HostedCluster"))
}

func TestSyncOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		cluster     *coreapi.Cluster
		dbResources []any
		setupCSMock func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec
		wantErr     bool
		verifyDB    func(t *testing.T, ctx context.Context, kaClient *kubeappliercosmosstoragetesting.MockKubeApplierDBClient)
	}{
		{
			name:    "skips cluster not found in cache",
			cluster: nil,
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			wantErr: false,
		},
		{
			name: "skips cluster without ClusterServiceID",
			cluster: newCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.ClusterServiceID = nil
			}),
			dbResources: []any{
				newSPC(testManagementClusterResourceID),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			wantErr: false,
		},
		{
			name: "skips cluster being deleted",
			cluster: newCluster(func(c *coreapi.Cluster) {
				now := metav1.Now()
				c.ServiceProviderProperties.DeletionTimestamp = &now
			}),
			dbResources: []any{
				newSPC(testManagementClusterResourceID),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			wantErr: false,
		},
		{
			name:    "returns error when GetClusterResources fails",
			cluster: newCluster(),
			dbResources: []any{
				newSPC(testManagementClusterResourceID),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					GetClusterResources(gomock.Any(), gomock.Any()).
					Return(nil, fmt.Errorf("connection refused"))
				return mock
			},
			wantErr: true,
		},
		{
			name:    "returns error on timestamp parsing errors from OCM SDK",
			cluster: newCluster(),
			dbResources: []any{
				newSPC(testManagementClusterResourceID),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					GetClusterResources(gomock.Any(), gomock.Any()).
					Return(nil, fmt.Errorf("parsing time \"invalid\" as creation_timestamp failed"))
				return mock
			},
			wantErr: true,
		},
		{
			name:    "no-op when GetClusterResources returns nil",
			cluster: newCluster(),
			dbResources: []any{
				newSPC(testManagementClusterResourceID),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					GetClusterResources(gomock.Any(), gomock.Any()).
					Return(nil, nil)
				return mock
			},
			wantErr: false,
		},
		{
			name:    "creates ApplyDesire for a single resource",
			cluster: newCluster(),
			dbResources: []any{
				newSPC(testManagementClusterResourceID),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				configMap := `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"external-auth-ca","namespace":"ocm-env-abc","labels":{"api.openshift.com/type":"ext-auth"}},"data":{"key":"value"}}`
				mock.EXPECT().
					GetClusterResources(gomock.Any(), gomock.Any()).
					Return(buildClusterResources(map[string]string{
						"default-ingress-configmap": configMap,
					}), nil)
				return mock
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				crud, err := kaClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
				require.NoError(t, err, "failed to get ApplyDesires CRUD")

				desire, err := crud.Get(ctx, "externalauthcaconfigmap")
				require.NoError(t, err, "ApplyDesire for ExternalAuthCAConfigMap should exist")

				assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, desire.Spec.Type, "desire type should be ServerSideApply")
				assert.Equal(t, testManagementClusterResourceID.String(), desire.Spec.ManagementCluster.String(), "management cluster should match")
				assert.Equal(t, "external-auth-ca", desire.Spec.TargetItem.Name, "target name should match")
				assert.Equal(t, "ocm-env-abc", desire.Spec.TargetItem.Namespace, "target namespace should match")
				assert.Equal(t, "configmaps", desire.Spec.TargetItem.Resource, "target resource should be the plural resource name")
				assert.NotNil(t, desire.Spec.ServerSideApply, "ServerSideApply config should be set")
				assert.NotNil(t, desire.Spec.ServerSideApply.KubeContent, "KubeContent should be set")
			},
		},
		{
			name:    "creates ApplyDesires for multiple resources",
			cluster: newCluster(),
			dbResources: []any{
				newSPC(testManagementClusterResourceID),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					GetClusterResources(gomock.Any(), gomock.Any()).
					Return(buildClusterResources(map[string]string{
						"pull-secret":          `{"apiVersion":"v1","kind":"Secret","metadata":{"name":"pull-secret","namespace":"ocm-env-abc"}}`,
						"external-auth-secret": `{"apiVersion":"v1","kind":"Secret","metadata":{"name":"external-auth-console-openshift-console","namespace":"ocm-env-abc","labels":{"api.openshift.com/type":"ext-auth"},"annotations":{"hypershift.openshift.io/hosted-cluster-sourced":"true"}}}`,
						"ingress-configmap":    `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"default-ingress","namespace":"ocm-env-abc"}}`,
						"external-auth-ca":     `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"external-auth-ca","namespace":"ocm-env-abc","labels":{"api.openshift.com/type":"ext-auth"}},"data":{"ca-bundle.crt":"test-ca"}}`,
					}), nil)
				return mock
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				crud, err := kaClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
				require.NoError(t, err, "failed to get ApplyDesires CRUD")

				desireA, err := crud.Get(ctx, "ocppullsecret")
				require.NoError(t, err, "ApplyDesire for OCPPullSecret should exist")
				assert.Equal(t, "pull-secret", desireA.Spec.TargetItem.Name, "secret target name should match")
				assert.Equal(t, "secrets", desireA.Spec.TargetItem.Resource, "secret target resource should match")

				externalAuthDesire, err := crud.Get(ctx, "externalauthclientsecret")
				require.NoError(t, err, "ApplyDesire for ExternalAuthClientSecret should exist")
				assert.Equal(t, "external-auth-console-openshift-console", externalAuthDesire.Spec.TargetItem.Name, "external auth secret target name should match")
				assert.Equal(t, "secrets", externalAuthDesire.Spec.TargetItem.Resource, "external auth secret target resource should match")

				_, err = crud.Get(ctx, "defaultingressconfigmap")
				require.Error(t, err, "ApplyDesire for DefaultIngressConfigMap should be suppressed; ingress is now managed by HyperShift")

				externalAuthCA, err := crud.Get(ctx, "externalauthcaconfigmap")
				require.NoError(t, err, "ApplyDesire for ExternalAuthCAConfigMap should exist")
				assert.Equal(t, "external-auth-ca", externalAuthCA.Spec.TargetItem.Name, "external auth CA target name should match")
				assert.Equal(t, "configmaps", externalAuthCA.Spec.TargetItem.Resource, "external auth CA target resource should match")
			},
		},
		{
			name:    "sets backend-managed annotations only on HostedCluster",
			cluster: newCluster(),
			dbResources: []any{
				newSPC(testManagementClusterResourceID),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					GetClusterResources(gomock.Any(), gomock.Any()).
					Return(buildClusterResources(map[string]string{
						"hostedcluster":     `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"HostedCluster","metadata":{"name":"test-cluster","namespace":"ocm-env-abc"}}`,
						"pull-secret":       `{"apiVersion":"v1","kind":"Secret","metadata":{"name":"pull-secret","namespace":"ocm-env-abc"}}`,
						"ingress-configmap": `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"external-auth-ca","namespace":"ocm-env-abc","labels":{"api.openshift.com/type":"ext-auth"}},"data":{"ca-bundle.crt":"test-ca"}}`,
					}), nil)
				return mock
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				crud, err := kaClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
				require.NoError(t, err, "failed to get ApplyDesires CRUD")

				// Verify HostedCluster has the annotation
				hcDesire, err := crud.Get(ctx, "hostedcluster")
				require.NoError(t, err, "ApplyDesire for HostedCluster should exist")

				var hcContent map[string]interface{}
				err = json.Unmarshal(hcDesire.Spec.ServerSideApply.KubeContent.Raw, &hcContent)
				require.NoError(t, err, "should unmarshal HostedCluster KubeContent")

				metadata, ok := hcContent["metadata"].(map[string]interface{})
				require.True(t, ok, "metadata field should be a map")

				annotations, ok := metadata["annotations"].(map[string]interface{})
				require.True(t, ok, "annotations field should be a map")

				assert.Equal(t, "10m", annotations["hypershift.openshift.io/machine-health-check-node-startup-timeout"],
					"HostedCluster should have the MHC annotation")

				// Verify Secret does NOT have the annotation
				secretDesire, err := crud.Get(ctx, "ocppullsecret")
				require.NoError(t, err, "ApplyDesire for Secret should exist")

				var secretContent map[string]interface{}
				err = json.Unmarshal(secretDesire.Spec.ServerSideApply.KubeContent.Raw, &secretContent)
				require.NoError(t, err, "should unmarshal Secret KubeContent")

				secretMetadata, ok := secretContent["metadata"].(map[string]interface{})
				require.True(t, ok, "secret metadata field should be a map")

				secretAnnotations, _ := secretMetadata["annotations"].(map[string]interface{})
				if secretAnnotations != nil {
					assert.NotContains(t, secretAnnotations, "hypershift.openshift.io/machine-health-check-node-startup-timeout",
						"Secret should NOT have the MHC annotation")
				}

				// Verify ConfigMap does NOT have the annotation
				cmDesire, err := crud.Get(ctx, "externalauthcaconfigmap")
				require.NoError(t, err, "ApplyDesire for ConfigMap should exist")

				var cmContent map[string]interface{}
				err = json.Unmarshal(cmDesire.Spec.ServerSideApply.KubeContent.Raw, &cmContent)
				require.NoError(t, err, "should unmarshal ConfigMap KubeContent")

				cmMetadata, ok := cmContent["metadata"].(map[string]interface{})
				require.True(t, ok, "configmap metadata field should be a map")

				cmAnnotations, _ := cmMetadata["annotations"].(map[string]interface{})
				if cmAnnotations != nil {
					assert.NotContains(t, cmAnnotations, "hypershift.openshift.io/machine-health-check-node-startup-timeout",
						"ConfigMap should NOT have the MHC annotation")
				}
			},
		},
		{
			name:    "removes null username prefix from HostedCluster ApplyDesire",
			cluster: newCluster(),
			dbResources: []any{
				newSPC(testManagementClusterResourceID),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					GetClusterResources(gomock.Any(), gomock.Any()).
					Return(buildClusterResources(map[string]string{
						"hostedcluster": `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"HostedCluster","metadata":{"name":"test-cluster","namespace":"ocm-env-abc"},"spec":{"configuration":{"authentication":{"oidcProviders":[{"name":"no-opinion","claimMappings":{"username":{"claim":"sub","prefixPolicy":"","prefix":null}}},{"name":"explicit","claimMappings":{"username":{"claim":"email","prefixPolicy":"Prefix","prefix":{"prefixString":"custom:"}}}}]}}}}`,
					}), nil)
				return mock
			},
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				crud, err := kaClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
				require.NoError(t, err)
				desire, err := crud.Get(ctx, "hostedcluster")
				require.NoError(t, err)

				var content map[string]interface{}
				require.NoError(t, json.Unmarshal(desire.Spec.ServerSideApply.KubeContent.Raw, &content))
				providers, found, err := unstructured.NestedSlice(content,
					"spec", "configuration", "authentication", "oidcProviders")
				require.NoError(t, err)
				require.True(t, found)
				require.Len(t, providers, 2)

				first := providers[0].(map[string]interface{})["claimMappings"].(map[string]interface{})["username"].(map[string]interface{})
				assert.NotContains(t, first, "prefix")
				assert.Equal(t, "", first["prefixPolicy"])

				second := providers[1].(map[string]interface{})["claimMappings"].(map[string]interface{})["username"].(map[string]interface{})
				assert.Equal(t, map[string]interface{}{"prefixString": "custom:"}, second["prefix"])
			},
		},
		{
			name:    "skips when management cluster is not placed",
			cluster: newCluster(),
			dbResources: []any{
				newSPC(nil),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			wantErr: false,
		},
		{
			name:    "replaces existing ApplyDesire when content changes",
			cluster: newCluster(),
			dbResources: []any{
				newSPC(testManagementClusterResourceID),
			},
			setupCSMock: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					GetClusterResources(gomock.Any(), gomock.Any()).
					Return(buildClusterResources(map[string]string{
						"ingress-configmap": `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"external-auth-ca","namespace":"ocm-env-abc","labels":{"api.openshift.com/type":"ext-auth"}},"data":{"key":"updated-value"}}`,
					}), nil)
				return mock
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, kaClient *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
				t.Helper()
				crud, err := kaClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
				require.NoError(t, err, "failed to get ApplyDesires CRUD")

				desire, err := crud.Get(ctx, "externalauthcaconfigmap")
				require.NoError(t, err, "ApplyDesire should exist after replacement")

				var content map[string]interface{}
				err = json.Unmarshal(desire.Spec.ServerSideApply.KubeContent.Raw, &content)
				require.NoError(t, err, "should unmarshal KubeContent")
				data, ok := content["data"].(map[string]interface{})
				require.True(t, ok, "data field should be a map")
				assert.Equal(t, "updated-value", data["key"], "KubeContent should reflect the updated value")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)

			mockKubeApplierDBClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
			mockKubeApplierClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
			mockKubeApplierDBClients.Register(testManagementClusterResourceID, mockKubeApplierClient)

			// Seed the cluster lister (used by SyncOnce to look up the cluster)
			clusterLister := &corelistertesting.SliceClusterLister{}
			if tt.cluster != nil {
				clusterLister.Clusters = []*coreapi.Cluster{tt.cluster}
			}

			// Seed the SPC lister from dbResources
			spcLister := &corelistertesting.SliceServiceProviderClusterLister{}
			for _, r := range tt.dbResources {
				if spc, ok := r.(*coreapi.ServiceProviderCluster); ok {
					spcLister.ServiceProviderClusters = append(spcLister.ServiceProviderClusters, spc)
				}
			}

			mcLister := &fleetlistertesting.SliceManagementClusterLister{
				ManagementClusters: []*fleetapi.ManagementCluster{
					{
						CosmosMetadata: coreapi.CosmosMetadata{
							ResourceID: testManagementClusterResourceID,
						},
					},
				},
			}

			syncer := &clusterResourcesController{
				clusterLister:                clusterLister,
				serviceProviderClusterLister: spcLister,
				clustersServiceClient:        tt.setupCSMock(ctrl),
				kubeApplierDBClients:         mockKubeApplierDBClients,
				applyDesireLister:            &kubeapplierlistertesting.DBApplyDesireLister{Clients: mockKubeApplierDBClients, Lister: mcLister},
				readDesireLister:             &kubeapplierlistertesting.DBReadDesireLister{Clients: mockKubeApplierDBClients, Lister: mcLister},
				resourcesCache:               lru.New(resourcesCacheCapacity),
				clock:                        utilsclock.RealClock{},
			}

			err := syncer.SyncOnce(ctx, testKey())
			if tt.wantErr {
				require.Error(t, err, "expected SyncOnce to return an error")
				return
			}
			require.NoError(t, err, "expected SyncOnce to succeed")
			if tt.verifyDB != nil {
				tt.verifyDB(t, ctx, mockKubeApplierClient)
			}
		})
	}
}

func TestGetClusterResourcesCache(t *testing.T) {
	t.Parallel()

	fixedNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clusterServiceID := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceID))
	otherClusterServiceID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/def456"))
	resources := buildClusterResources(map[string]string{
		"ingress-configmap": `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"default-ingress","namespace":"ocm-env-abc"}}`,
	})
	updated := buildClusterResources(map[string]string{
		"ingress-configmap": `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"default-ingress","namespace":"ocm-env-abc"},"data":{"key":"updated"}}`,
	})

	newSyncer := func(t *testing.T, client ocm.ClusterServiceClientSpec) (*clusterResourcesController, *clocktesting.FakePassiveClock) {
		t.Helper()
		clk := clocktesting.NewFakePassiveClock(fixedNow)
		return &clusterResourcesController{
			clustersServiceClient: client,
			resourcesCache:        lru.New(resourcesCacheCapacity),
			clock:                 clk,
		}, clk
	}

	t.Run("reuses a successful response until the ttl elapses", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		ctrl := gomock.NewController(t)
		mock := ocm.NewMockClusterServiceClientSpec(ctrl)
		gomock.InOrder(
			mock.EXPECT().GetClusterResources(gomock.Any(), clusterServiceID).Return(resources, nil),
			mock.EXPECT().GetClusterResources(gomock.Any(), clusterServiceID).Return(updated, nil),
		)
		syncer, clk := newSyncer(t, mock)

		got, err := syncer.getClusterResources(ctx, clusterServiceID)
		require.NoError(t, err, "first fetch should succeed")
		require.Equal(t, resources, got, "first fetch should return the Cluster Service response")

		clk.SetTime(fixedNow.Add(resourcesCacheTTL - time.Nanosecond))
		got, err = syncer.getClusterResources(ctx, clusterServiceID)
		require.NoError(t, err, "fetch inside the ttl should succeed")
		require.Equal(t, resources, got, "fetch inside the ttl should return the cached response")

		clk.SetTime(fixedNow.Add(resourcesCacheTTL))
		got, err = syncer.getClusterResources(ctx, clusterServiceID)
		require.NoError(t, err, "fetch at the ttl boundary should succeed")
		require.Equal(t, updated, got, "fetch at the ttl boundary should return the fresh response")
	})

	t.Run("does not cache errors", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		ctrl := gomock.NewController(t)
		mock := ocm.NewMockClusterServiceClientSpec(ctrl)
		gomock.InOrder(
			mock.EXPECT().GetClusterResources(gomock.Any(), clusterServiceID).Return(nil, fmt.Errorf("connection refused")),
			mock.EXPECT().GetClusterResources(gomock.Any(), clusterServiceID).Return(resources, nil),
		)
		syncer, _ := newSyncer(t, mock)

		_, err := syncer.getClusterResources(ctx, clusterServiceID)
		require.Error(t, err, "a failed fetch should be returned")
		got, err := syncer.getClusterResources(ctx, clusterServiceID)
		require.NoError(t, err, "the retry after an error should call Cluster Service again")
		require.Equal(t, resources, got, "the retry should return the fresh response")
	})

	t.Run("caches each cluster service id separately", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		ctrl := gomock.NewController(t)
		mock := ocm.NewMockClusterServiceClientSpec(ctrl)
		other := buildClusterResources(map[string]string{
			"pull-secret": `{"apiVersion":"v1","kind":"Secret","metadata":{"name":"pull-secret","namespace":"ocm-env-def"}}`,
		})
		mock.EXPECT().GetClusterResources(gomock.Any(), clusterServiceID).Return(resources, nil)
		mock.EXPECT().GetClusterResources(gomock.Any(), otherClusterServiceID).Return(other, nil)
		syncer, _ := newSyncer(t, mock)

		got, err := syncer.getClusterResources(ctx, clusterServiceID)
		require.NoError(t, err, "first cluster fetch should succeed")
		require.Equal(t, resources, got, "first cluster should return its own response")
		got, err = syncer.getClusterResources(ctx, otherClusterServiceID)
		require.NoError(t, err, "second cluster fetch should succeed")
		require.Equal(t, other, got, "second cluster should return its own response")

		got, err = syncer.getClusterResources(ctx, clusterServiceID)
		require.NoError(t, err, "cached first cluster fetch should succeed")
		require.Equal(t, resources, got, "cached first cluster should not reuse the other cluster's response")
		got, err = syncer.getClusterResources(ctx, otherClusterServiceID)
		require.NoError(t, err, "cached second cluster fetch should succeed")
		require.Equal(t, other, got, "cached second cluster should not reuse the first cluster's response")
	})
}

func TestDeleteStaleApplyDesires(t *testing.T) {
	t.Parallel()

	makeTaggedDesire := func(name string) *kubeapplierapi.ApplyDesire {
		resourceIDStr := kubeapplierapihelpers.ToClusterScopedApplyDesireResourceIDString(
			testSubscriptionID, testResourceGroupName, testClusterName, name,
		)
		return &kubeapplierapi.ApplyDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(resourceIDStr)),
				PartitionKey: strings.ToLower(testManagementClusterResourceID.String()),
			},
			Tags: map[string]string{kubeapplierapi.TagControllerName: ClusterResourcesControllerName},
			Spec: kubeapplierapi.ApplyDesireSpec{
				ManagementCluster: testManagementClusterResourceID,
				Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
				TargetItem: kubeapplierapi.ResourceReference{
					Group: "", Version: "v1", Resource: "configmaps",
					Name: name, Namespace: "ns",
				},
				ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
					KubeContent: &runtime.RawExtension{Raw: []byte(`{}`)},
				},
			},
		}
	}

	makeUntaggedDesire := func(name string) *kubeapplierapi.ApplyDesire {
		d := makeTaggedDesire(name)
		d.Tags = nil
		return d
	}

	t.Run("marks stale tagged desire for deletion", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

		mockKubeApplierClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(testManagementClusterResourceID, mockKubeApplierClient)

		crud, _ := mockKubeApplierClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
		_, _ = crud.Create(ctx, makeTaggedDesire("configmaps.ns.current"), nil)
		_, _ = crud.Create(ctx, makeTaggedDesire("configmaps.ns.stale"), nil)

		mcLister := &fleetlistertesting.SliceManagementClusterLister{
			ManagementClusters: []*fleetapi.ManagementCluster{
				{
					CosmosMetadata: coreapi.CosmosMetadata{
						ResourceID: testManagementClusterResourceID,
					},
				},
			},
		}
		syncer := &clusterResourcesController{
			kubeApplierDBClients: mockClients,
			applyDesireLister:    &kubeapplierlistertesting.DBApplyDesireLister{Clients: mockClients, Lister: mcLister},
			readDesireLister:     &kubeapplierlistertesting.DBReadDesireLister{Clients: mockClients, Lister: mcLister},
		}

		currentResourceID := kubeapplierapihelpers.ToClusterScopedApplyDesireResourceIDString(
			testSubscriptionID, testResourceGroupName, testClusterName, "configmaps.ns.current",
		)
		err := syncer.deleteStaleApplyDesires(ctx, testKey(), testManagementClusterResourceID,
			map[string]bool{currentResourceID: true})
		require.NoError(t, err, "deleteStaleApplyDesires should succeed")

		stale, err := crud.Get(ctx, "configmaps.ns.stale")
		require.NoError(t, err, "stale desire should still exist")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, stale.Spec.Type, "stale desire should be marked for deletion")
		assert.Nil(t, stale.Spec.ServerSideApply, "ServerSideApply should be cleared")

		current, err := crud.Get(ctx, "configmaps.ns.current")
		require.NoError(t, err, "current desire should still exist")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, current.Spec.Type, "current desire should be unchanged")
	})

	t.Run("does not touch untagged desires", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

		mockKubeApplierClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(testManagementClusterResourceID, mockKubeApplierClient)

		crud, _ := mockKubeApplierClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
		_, _ = crud.Create(ctx, makeUntaggedDesire("configmaps.ns.other-controller"), nil)

		mcLister := &fleetlistertesting.SliceManagementClusterLister{
			ManagementClusters: []*fleetapi.ManagementCluster{
				{
					CosmosMetadata: coreapi.CosmosMetadata{
						ResourceID: testManagementClusterResourceID,
					},
				},
			},
		}
		syncer := &clusterResourcesController{
			kubeApplierDBClients: mockClients,
			applyDesireLister:    &kubeapplierlistertesting.DBApplyDesireLister{Clients: mockClients, Lister: mcLister},
			readDesireLister:     &kubeapplierlistertesting.DBReadDesireLister{Clients: mockClients, Lister: mcLister},
		}

		err := syncer.deleteStaleApplyDesires(ctx, testKey(), testManagementClusterResourceID, map[string]bool{})
		require.NoError(t, err, "deleteStaleApplyDesires should succeed")

		desire, err := crud.Get(ctx, "configmaps.ns.other-controller")
		require.NoError(t, err, "untagged desire should still exist")
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, desire.Spec.Type, "untagged desire should be untouched")
	})

	t.Run("waits for Delete-type desire without successful condition", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

		mockKubeApplierClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(testManagementClusterResourceID, mockKubeApplierClient)

		crud, _ := mockKubeApplierClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)

		staleDesire := makeTaggedDesire("nodepools.ns.pending")
		staleDesire.Spec.Type = kubeapplierapi.ApplyDesireTypeDelete
		staleDesire.Spec.ServerSideApply = nil
		_, _ = crud.Create(ctx, staleDesire, nil)

		mcLister := &fleetlistertesting.SliceManagementClusterLister{
			ManagementClusters: []*fleetapi.ManagementCluster{
				{
					CosmosMetadata: coreapi.CosmosMetadata{
						ResourceID: testManagementClusterResourceID,
					},
				},
			},
		}
		syncer := &clusterResourcesController{
			kubeApplierDBClients: mockClients,
			applyDesireLister:    &kubeapplierlistertesting.DBApplyDesireLister{Clients: mockClients, Lister: mcLister},
			readDesireLister:     &kubeapplierlistertesting.DBReadDesireLister{Clients: mockClients, Lister: mcLister},
		}

		err := syncer.deleteStaleApplyDesires(ctx, testKey(), testManagementClusterResourceID, map[string]bool{})
		require.NoError(t, err, "deleteStaleApplyDesires should succeed")

		_, err = crud.Get(ctx, "nodepools.ns.pending")
		require.NoError(t, err, "desire should still exist while delete is pending")
	})

	t.Run("purges Delete-type desire when SuccessfullyDeleted condition is true", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

		mockKubeApplierClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(testManagementClusterResourceID, mockKubeApplierClient)

		crud, _ := mockKubeApplierClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)

		staleDesire := makeTaggedDesire("configmaps.ns.gone")
		staleDesire.Spec.Type = kubeapplierapi.ApplyDesireTypeDelete
		staleDesire.Spec.ServerSideApply = nil
		staleDesire.Status.Conditions = []metav1.Condition{
			{Type: kubeapplierapi.ConditionTypeSuccessfullyDeleted, Status: metav1.ConditionTrue},
		}
		_, _ = crud.Create(ctx, staleDesire, nil)

		mcLister := &fleetlistertesting.SliceManagementClusterLister{
			ManagementClusters: []*fleetapi.ManagementCluster{
				{
					CosmosMetadata: coreapi.CosmosMetadata{
						ResourceID: testManagementClusterResourceID,
					},
				},
			},
		}
		syncer := &clusterResourcesController{
			kubeApplierDBClients: mockClients,
			applyDesireLister:    &kubeapplierlistertesting.DBApplyDesireLister{Clients: mockClients, Lister: mcLister},
			readDesireLister:     &kubeapplierlistertesting.DBReadDesireLister{Clients: mockClients, Lister: mcLister},
		}

		err := syncer.deleteStaleApplyDesires(ctx, testKey(), testManagementClusterResourceID, map[string]bool{})
		require.NoError(t, err, "deleteStaleApplyDesires should succeed")

		_, err = crud.Get(ctx, "configmaps.ns.gone")
		assert.Error(t, err, "purged ApplyDesire should no longer exist")
	})
}

func TestDeleteStaleLegacyIngressDesires(t *testing.T) {
	t.Parallel()

	for _, desireName := range []string{
		DesireNameManagedCluster,
		DesireNameDefaultIngressConfigMap,
		DesireNameDefaultIngressWildcardCertSecretSync,
		DesireNameDefaultIngressWildcardCertSecretProviderClass,
	} {
		t.Run(desireName, func(t *testing.T) {
			t.Parallel()
			for _, variant := range []string{desireName, strings.ToLower(desireName), strings.ToUpper(desireName)} {
				require.True(t, legacyACMBasedIngressCertificateWiringDesireName(variant), "creation suppression must recognize %s", variant)
			}
			for _, scenario := range []struct {
				name    string
				tags    map[string]string
				current bool
			}{
				{name: "owned stale", tags: map[string]string{kubeapplierapi.TagControllerName: ClusterResourcesControllerName}},
				{name: "owned current", tags: map[string]string{kubeapplierapi.TagControllerName: ClusterResourcesControllerName}, current: true},
				{name: "untagged"},
				{name: "missing controller tag", tags: map[string]string{"unrelated": "value"}},
				{name: "other controller", tags: map[string]string{kubeapplierapi.TagControllerName: "OtherController"}},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					t.Parallel()
					ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
					desire := newOwnedClusterDesire(desireName)
					desire.Tags = scenario.tags
					desire.Status.Conditions = []metav1.Condition{{Type: kubeapplierapi.ConditionTypeSuccessfullyDeleted, Status: metav1.ConditionTrue}}
					fixture := newDestructFixtureWithReadDesires(t, ctx, "nodepool", []*kubeapplierapi.ApplyDesire{desire}, desireName)
					applyCRUD, err := fixture.mockKubeApplierClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
					require.NoError(t, err)
					readCRUD, err := fixture.mockKubeApplierClient.ReadDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
					require.NoError(t, err)
					originalRead, err := readCRUD.Get(ctx, desireName)
					require.NoError(t, err)
					currentDesired := map[string]bool{}
					if scenario.current {
						currentDesired[strings.ToLower(desire.ResourceID.String())] = true
					}
					reconcile := func() {
						t.Helper()
						require.NoError(t, fixture.controller.deleteStaleApplyDesires(ctx, testKey(), testManagementClusterResourceID, currentDesired))
					}

					reconcile()
					actual, err := applyCRUD.Get(ctx, desireName)
					require.NoError(t, err, "ApplyDesire must remain until deletion completes")
					if scenario.current || scenario.tags[kubeapplierapi.TagControllerName] != ClusterResourcesControllerName {
						require.Equal(t, desire.Spec, actual.Spec, "protected desire must be unchanged")
						require.Equal(t, desire.Tags, actual.Tags, "ownership must be unchanged")
						require.Equal(t, desire.Status, actual.Status, "protected status must be unchanged")
						actualRead, err := readCRUD.Get(ctx, desireName)
						require.NoError(t, err)
						require.Equal(t, originalRead, actualRead, "protected ReadDesire must be unchanged")
						return
					}
					require.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, actual.Spec.Type)
					require.Nil(t, actual.Spec.ServerSideApply)
					require.Empty(t, actual.Status.Conditions, "stale completion must not confirm the new delete")
					require.Equal(t, desire.Spec.TargetItem, actual.Spec.TargetItem, "deletion target must be retained")

					for _, conditions := range [][]metav1.Condition{
						nil,
						{{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionTrue}},
					} {
						actual.Status.Conditions = conditions
						_, err = applyCRUD.Replace(ctx, actual, nil)
						require.NoError(t, err)
						reconcile()
						actual, err = applyCRUD.Get(ctx, desireName)
						require.NoError(t, err, "pending deletion must retain ApplyDesire")
						require.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, actual.Spec.Type)
						actualRead, err := readCRUD.Get(ctx, desireName)
						require.NoError(t, err, "pending deletion must retain ReadDesire")
						require.Equal(t, originalRead, actualRead)
					}

					actual.Status.Conditions = []metav1.Condition{{Type: kubeapplierapi.ConditionTypeSuccessfullyDeleted, Status: metav1.ConditionTrue}}
					_, err = applyCRUD.Replace(ctx, actual, nil)
					require.NoError(t, err)
					reconcile()
					_, err = applyCRUD.Get(ctx, desireName)
					require.True(t, cosmosstorageutils.IsNotFoundError(err), "completed ApplyDesire must be purged: %v", err)
					_, err = readCRUD.Get(ctx, desireName)
					require.True(t, cosmosstorageutils.IsNotFoundError(err), "corresponding ReadDesire must be purged: %v", err)
					reconcile()
				})
			}
		})
	}
}

func TestClassifyClusterResource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		resource     string
		wantDesire   string
		wantNodePool string
		wantErr      bool
	}{
		{
			name:       "HostedCluster",
			resource:   `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"HostedCluster","metadata":{"name":"my-hc","namespace":"ocm-env-abc"}}`,
			wantDesire: "HostedCluster",
		},
		{
			name:         "NodePool with spec.clusterName prefix",
			resource:     `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"NodePool","metadata":{"name":"q2e1p3b8m8a3s6l-np-2dz967","namespace":"ocm-env-abc"},"spec":{"clusterName":"q2e1p3b8m8a3s6l"}}`,
			wantDesire:   "NodePool",
			wantNodePool: "np-2dz967",
		},
		{
			name:         "NodePool without spec.clusterName",
			resource:     `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"NodePool","metadata":{"name":"worker","namespace":"ocm-env-abc"}}`,
			wantDesire:   "NodePool",
			wantNodePool: "worker",
		},
		{
			name:       "HostedCluster Namespace",
			resource:   `{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"ocm-arohcppers-2sdm6b8jke9sm3h8ukc8mbaahngnre5c","labels":{"api.openshift.com/environment":"arohcppers","api.openshift.com/id":"2sdm6b8jke9sm3h8ukc8mbaahngnre5c","api.openshift.com/name":"cluster-hs-pre-rxhvsd"}}}`,
			wantDesire: "HostedClusterNamespace",
		},
		{
			name:       "ControlPlane Namespace",
			resource:   `{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"ocm-arohcppers-2sdm6b8jke9sm3h8ukc8mbaahngnre5c-j7h3t4w0u1t3b4b","labels":{"app.kubernetes.io/managed-by":"aro-hcp-clusters-service","hypershift.openshift.io/cluster":"2sdm6b8jke9sm3h8ukc8mbaahngnre5c"}}}`,
			wantDesire: "ControlPlaneNamespace",
		},
		{
			name:       "ConfigMap → DefaultIngressConfigMap",
			resource:   `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"default-ingress","namespace":"ocm-env-abc"}}`,
			wantDesire: "DefaultIngressConfigMap",
		},
		{
			name:       "ext-auth labeled ConfigMap → ExternalAuthCAConfigMap",
			resource:   `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"external-auth-ca","namespace":"ocm-env-abc","labels":{"api.openshift.com/type":"ext-auth"}}}`,
			wantDesire: "ExternalAuthCAConfigMap",
		},
		{
			name:       "Secret → OCPPullSecret",
			resource:   `{"apiVersion":"v1","kind":"Secret","metadata":{"name":"pull-secret","namespace":"ocm-env-abc"}}`,
			wantDesire: "OCPPullSecret",
		},
		{
			name:       "ext-auth labeled Secret → ExternalAuthClientSecret",
			resource:   `{"apiVersion":"v1","kind":"Secret","metadata":{"name":"external-auth-console-openshift-console","namespace":"ocm-env-abc","labels":{"api.openshift.com/type":"ext-auth"}}}`,
			wantDesire: "ExternalAuthClientSecret",
		},
		{
			name:       "PodNetwork",
			resource:   `{"apiVersion":"multitenancy.acn.azure.com/v1alpha1","kind":"PodNetwork","metadata":{"name":"pn-abc123"}}`,
			wantDesire: "PodNetwork",
		},
		{
			name:       "PodNetworkInstance",
			resource:   `{"apiVersion":"multitenancy.acn.azure.com/v1alpha1","kind":"PodNetworkInstance","metadata":{"name":"pni-abc123","namespace":"ocm-env-abc-cp"}}`,
			wantDesire: "PodNetworkInstance",
		},
		{
			name:       "SecretSync bound-sa-signing-key",
			resource:   `{"apiVersion":"secret-sync.x-k8s.io/v1alpha1","kind":"SecretSync","metadata":{"name":"bound-sa-signing-key","namespace":"ocm-env-abc"}}`,
			wantDesire: "BoundServiceAccountSigningKeySecretSync",
		},
		{
			name:       "SecretSync bound-service-account-signing-key",
			resource:   `{"apiVersion":"secret-sync.x-k8s.io/v1alpha1","kind":"SecretSync","metadata":{"name":"bound-service-account-signing-key","namespace":"ocm-env-abc"}}`,
			wantDesire: "BoundServiceAccountSigningKeySecretSync",
		},
		{
			name:       "SecretSync default-ingress",
			resource:   `{"apiVersion":"secret-sync.x-k8s.io/v1alpha1","kind":"SecretSync","metadata":{"name":"default-ingress-cert","namespace":"ocm-env-abc"}}`,
			wantDesire: "DefaultIngressWildcardCertSecretSync",
		},
		{
			name:       "SecretSync kube-apiserver",
			resource:   `{"apiVersion":"secret-sync.x-k8s.io/v1alpha1","kind":"SecretSync","metadata":{"name":"kube-apiserver-server-cert","namespace":"ocm-env-abc"}}`,
			wantDesire: "KubeAPIServerServingCertSecretSync",
		},
		{
			name:       "SecretProviderClass bound-sa-signing-key",
			resource:   `{"apiVersion":"secrets-store.csi.x-k8s.io/v1","kind":"SecretProviderClass","metadata":{"name":"bound-sa-signing-key","namespace":"ocm-env-abc"}}`,
			wantDesire: "BoundServiceAccountSigningKeySecretProviderClass",
		},
		{
			name:       "SecretProviderClass bound-service-account-signing-key",
			resource:   `{"apiVersion":"secrets-store.csi.x-k8s.io/v1","kind":"SecretProviderClass","metadata":{"name":"bound-service-account-signing-key","namespace":"ocm-env-abc"}}`,
			wantDesire: "BoundServiceAccountSigningKeySecretProviderClass",
		},
		{
			name:       "SecretProviderClass default-ingress",
			resource:   `{"apiVersion":"secrets-store.csi.x-k8s.io/v1","kind":"SecretProviderClass","metadata":{"name":"default-ingress-cert","namespace":"ocm-env-abc"}}`,
			wantDesire: "DefaultIngressWildcardCertSecretProviderClass",
		},
		{
			name:       "SecretProviderClass kube-apiserver",
			resource:   `{"apiVersion":"secrets-store.csi.x-k8s.io/v1","kind":"SecretProviderClass","metadata":{"name":"kube-apiserver-server-cert","namespace":"ocm-env-abc"}}`,
			wantDesire: "KubeAPIServerServingCertSecretProviderClass",
		},
		{
			name:     "unrecognized kind errors",
			resource: `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"web","namespace":"app"}}`,
			wantErr:  true,
		},
		{
			name:     "unrecognized SecretSync name errors",
			resource: `{"apiVersion":"secret-sync.x-k8s.io/v1alpha1","kind":"SecretSync","metadata":{"name":"unknown-sync","namespace":"ns"}}`,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var obj unstructured.Unstructured
			err := json.Unmarshal([]byte(tt.resource), &obj)
			require.NoError(t, err, "failed to unmarshal test resource")

			classified, err := classifyClusterResource(&obj)
			if tt.wantErr {
				require.Error(t, err, "expected classifyClusterResource to error")
				return
			}
			require.NoError(t, err, "classifyClusterResource should not error")
			assert.Equal(t, tt.wantDesire, classified.desireName, "desireName mismatch")
			assert.Equal(t, tt.wantNodePool, classified.nodePoolName, "nodePoolName mismatch")
		})
	}
}

// TestProcessClusterResourcesNodePoolPath covers the node-pool-scoped branch of
// processClusterResources. A NodePool marked for deletion is dropped from the
// desired set and its ApplyDesire is reaped, even while Cluster Service is
// still reporting the CR. That is deliberate: NodePoolClusterServiceDeleteDispatch
// does not wait on these desires, so the reap cannot deadlock against the
// work-agent re-applying the CR from the still-live ManifestWork.
func TestProcessClusterResourcesNodePoolPath(t *testing.T) {
	t.Parallel()

	const testNodePoolName = "gpu-np-1"

	nodePoolCR := `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"NodePool",` +
		`"metadata":{"name":"` + testClusterName + `-` + testNodePoolName + `","namespace":"ocm-env-abc"},` +
		`"spec":{"clusterName":"` + testClusterName + `"}}`

	newNodePool := func(deleting bool) *coreapi.NodePool {
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
				"/nodePools/" + testNodePoolName,
		))
		np := &coreapi.NodePool{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(resourceID.SubscriptionID),
			},
			TrackedResource: coreapi.TrackedResource{
				Resource: coreapi.Resource{
					ID:   resourceID,
					Name: testNodePoolName,
					Type: resourceID.ResourceType.String(),
				},
			},
		}
		np.ServiceProviderProperties.ClusterServiceID = metadataapihelpers.Ptr(metadataapi.Must(
			metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123/node_pools/np123")))
		if deleting {
			now := metav1.Now()
			np.ServiceProviderProperties.DeletionTimestamp = &now
		}
		return np
	}

	tests := []struct {
		name           string
		nodePool       *coreapi.NodePool
		wantDesireType kubeapplierapi.ApplyDesireType
		reason         string
	}{
		{
			name:           "keeps desire applied while NodePool is live",
			nodePool:       newNodePool(false),
			wantDesireType: kubeapplierapi.ApplyDesireTypeServerSideApply,
			reason:         "a live NodePool must keep its ApplyDesire applied",
		},
		{
			name:           "reaps desire once NodePool is marked for deletion",
			nodePool:       newNodePool(true),
			wantDesireType: kubeapplierapi.ApplyDesireTypeDelete,
			reason:         "a deleting NodePool is dropped from the desired set and reaped",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			mockKubeApplierClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
			mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
			mockClients.Register(testManagementClusterResourceID, mockKubeApplierClient)

			npCRUD, err := mockKubeApplierClient.ApplyDesiresForNodePool(
				testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
			require.NoError(t, err, "failed to get node pool ApplyDesires CRUD")

			mcLister := &fleetlistertesting.SliceManagementClusterLister{
				ManagementClusters: []*fleetapi.ManagementCluster{
					{
						CosmosMetadata: coreapi.CosmosMetadata{
							ResourceID: testManagementClusterResourceID,
						},
					},
				},
			}
			syncer := &clusterResourcesController{
				nodePoolLister:       &corelistertesting.SliceNodePoolLister{NodePools: []*coreapi.NodePool{tt.nodePool}},
				kubeApplierDBClients: mockClients,
				applyDesireLister:    &kubeapplierlistertesting.DBApplyDesireLister{Clients: mockClients, Lister: mcLister},
				readDesireLister:     &kubeapplierlistertesting.DBReadDesireLister{Clients: mockClients, Lister: mcLister},
			}

			// Seed the desire with a pass over a healthy NodePool, so the deletion
			// case exercises reaping an existing desire rather than never creating one.
			seedSyncer := *syncer
			seedSyncer.nodePoolLister = &corelistertesting.SliceNodePoolLister{
				NodePools: []*coreapi.NodePool{newNodePool(false)},
			}
			require.NoError(t, seedSyncer.processClusterResources(ctx, testKey(), testManagementClusterResourceID,
				buildClusterResources(map[string]string{"node-pool": nodePoolCR}), "abc123", nil),
				"seeding the NodePool ApplyDesire should succeed")
			_, err = npCRUD.Get(ctx, "nodepool")
			require.NoError(t, err, "seed pass should have created the NodePool ApplyDesire")

			// Cluster Service still reports the CR, as it does until it removes the
			// ManifestWork.
			require.NoError(t, syncer.processClusterResources(ctx, testKey(), testManagementClusterResourceID,
				buildClusterResources(map[string]string{"node-pool": nodePoolCR}), "abc123", nil),
				"processClusterResources should succeed")

			desire, err := npCRUD.Get(ctx, "nodepool")
			require.NoError(t, err, "NodePool ApplyDesire should still exist: %s", tt.reason)
			assert.Equal(t, tt.wantDesireType, desire.Spec.Type, "unexpected desire type: %s", tt.reason)
		})
	}
}

func TestEnsureHostedClusterAnnotations(t *testing.T) {
	tests := []struct {
		name                string
		inputAnnotations    map[string]string
		expectedAnnotations map[string]string
	}{
		{
			name:             "sets mhc node startup timeout annotation to 10m",
			inputAnnotations: nil,
			expectedAnnotations: map[string]string{
				hsv1beta1.MachineHealthCheckNodeStartupTimeoutAnnotation: "10m",
			},
		},
		{
			name: "preserves other annotations when setting mhc node startup timeout annotation",
			inputAnnotations: map[string]string{
				"other-annotation":                            "other-value",
				hsv1beta1.ClusterSizeOverrideAnnotation:       "e2e_minimal",
				hsv1beta1.ControlPlaneOperatorImageAnnotation: "quay.io/example/cpo:test",
			},
			expectedAnnotations: map[string]string{
				"other-annotation":                                       "other-value",
				hsv1beta1.ClusterSizeOverrideAnnotation:                  "e2e_minimal",
				hsv1beta1.ControlPlaneOperatorImageAnnotation:            "quay.io/example/cpo:test",
				hsv1beta1.MachineHealthCheckNodeStartupTimeoutAnnotation: "10m",
			},
		},
		{
			name: "overwrites existing mhc node startup timeout annotation with 10m",
			inputAnnotations: map[string]string{
				hsv1beta1.MachineHealthCheckNodeStartupTimeoutAnnotation: "20m",
				"other-annotation": "other-value",
			},
			expectedAnnotations: map[string]string{
				hsv1beta1.MachineHealthCheckNodeStartupTimeoutAnnotation: "10m",
				"other-annotation": "other-value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostedCluster := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "hypershift.openshift.io/v1beta1",
					"kind":       "HostedCluster",
					"metadata": map[string]interface{}{
						"name":      testClusterName,
						"namespace": "test-namespace",
					},
				},
			}

			if tt.inputAnnotations != nil {
				hostedCluster.SetAnnotations(tt.inputAnnotations)
			}

			err := ensureHostedClusterAnnotations(hostedCluster)
			require.NoError(t, err)

			actualAnnotations := hostedCluster.GetAnnotations()
			assert.Equal(t, tt.expectedAnnotations, actualAnnotations,
				"annotations should match expected")
		})
	}
}

func TestEnsureHostedClusterAutoscaling(t *testing.T) {
	hc := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "hypershift.openshift.io/v1beta1",
			"kind":       "HostedCluster",
			"metadata": map[string]interface{}{
				"name":      testClusterName,
				"namespace": "test-namespace",
			},
			"spec": map[string]interface{}{},
		},
	}

	err := ensureHostedClusterAutoscaling(hc)
	require.NoError(t, err)

	qps, found, err := unstructured.NestedFieldNoCopy(hc.Object, "spec", "autoscaling", "kubeClientQPS")
	require.NoError(t, err)
	assert.True(t, found, "expected spec.autoscaling.kubeClientQPS to be present")
	assert.Equal(t, int64(defaultKubeClientQPS), qps, "kubeClientQPS should be pinned to fleet default")

	burst, found, err := unstructured.NestedFieldNoCopy(hc.Object, "spec", "autoscaling", "kubeClientBurst")
	require.NoError(t, err)
	assert.True(t, found, "expected spec.autoscaling.kubeClientBurst to be present")
	assert.Equal(t, int64(defaultKubeClientBurst), burst, "kubeClientBurst should be pinned to fleet default")
}

func TestHostedClusterOverlayNotReadyDoesNotDelete(t *testing.T) {
	t.Parallel()
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	// Seed an existing HostedCluster ApplyDesire that was previously created
	// and is still desired — it must survive even when overlay is not ready.
	existingDesireName := DesireNameHostedCluster
	existingResourceIDStr := kubeapplierapihelpers.ToClusterScopedApplyDesireResourceIDString(
		testSubscriptionID, testResourceGroupName, testClusterName, existingDesireName,
	)
	existingDesire := &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(existingResourceIDStr)),
			PartitionKey: strings.ToLower(testManagementClusterResourceID.String()),
		},
		Tags: map[string]string{kubeapplierapi.TagControllerName: ClusterResourcesControllerName},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: testManagementClusterResourceID,
			Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
			TargetItem: kubeapplierapi.ResourceReference{
				Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "hostedclusters",
				Name: testClusterName, Namespace: "ns",
			},
			ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
				KubeContent: &runtime.RawExtension{Raw: []byte(`{}`)},
			},
		},
	}

	mockKubeApplierClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	mockClients.Register(testManagementClusterResourceID, mockKubeApplierClient)

	crud, err := mockKubeApplierClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	require.NoError(t, err)
	_, err = crud.Create(ctx, existingDesire, nil)
	require.NoError(t, err)

	// Also seed a stale desire that should still be reaped.
	staleName := "configmaps.ns.stale"
	staleResourceIDStr := kubeapplierapihelpers.ToClusterScopedApplyDesireResourceIDString(
		testSubscriptionID, testResourceGroupName, testClusterName, staleName,
	)
	staleDesire := &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(staleResourceIDStr)),
			PartitionKey: strings.ToLower(testManagementClusterResourceID.String()),
		},
		Tags: map[string]string{kubeapplierapi.TagControllerName: ClusterResourcesControllerName},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: testManagementClusterResourceID,
			Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
			TargetItem: kubeapplierapi.ResourceReference{
				Group: "", Version: "v1", Resource: "configmaps",
				Name: staleName, Namespace: "ns",
			},
			ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
				KubeContent: &runtime.RawExtension{Raw: []byte(`{}`)},
			},
		},
	}
	_, err = crud.Create(ctx, staleDesire, nil)
	require.NoError(t, err)

	// SPC with overlay not ready: DesiredHostedCluster.Spec.Release.Image nil -> overlay returns ready=false.
	spc := newSPC(testManagementClusterResourceID)
	// Use empty release image to trigger not-ready path.
	spc.Spec.DesiredHostedCluster.Spec.Release.Image = ""

	cluster := newCluster()

	// CS still reports HostedCluster as desired.
	resources := buildClusterResources(map[string]string{
		"hosted": `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"HostedCluster","metadata":{"name":"test-cluster","namespace":"ns"}}`,
	})

	ctrl := gomock.NewController(t)
	csClient := ocm.NewMockClusterServiceClientSpec(ctrl)
	csClient.EXPECT().GetClusterResources(gomock.Any(), gomock.Any()).Return(resources, nil)

	syncer := &clusterResourcesController{
		clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}},
		serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{spc}},
		clustersServiceClient:        csClient,
		kubeApplierDBClients:         mockClients,
		applyDesireLister: &kubeapplierlistertesting.DBApplyDesireLister{
			Clients: mockClients,
			Lister: &fleetlistertesting.SliceManagementClusterLister{
				ManagementClusters: []*fleetapi.ManagementCluster{
					{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: testManagementClusterResourceID}},
				},
			},
		},
		readDesireLister: &kubeapplierlistertesting.DBReadDesireLister{
			Clients: mockClients,
			Lister: &fleetlistertesting.SliceManagementClusterLister{
				ManagementClusters: []*fleetapi.ManagementCluster{
					{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: testManagementClusterResourceID}},
				},
			},
		},
		resourcesCache: lru.New(resourcesCacheCapacity),
		clock:          utilsclock.RealClock{},
	}

	err = syncer.SyncOnce(ctx, testKey())
	require.NoError(t, err, "SyncOnce should succeed even when HostedCluster overlay is not ready")

	// HostedCluster must NOT have been marked for deletion — it was still desired.
	preserved, err := crud.Get(ctx, existingDesireName)
	require.NoError(t, err, "HostedCluster ApplyDesire should still exist")
	assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, preserved.Spec.Type,
		"HostedCluster still desired but not processable must not be marked for deletion")
	assert.NotNil(t, preserved.Spec.ServerSideApply, "HostedCluster ServerSideApply must not be cleared")

	// Stale desire should still have been marked for deletion.
	stale, err := crud.Get(ctx, staleName)
	require.NoError(t, err, "stale desire should still exist (marked for deletion, not purged yet)")
	assert.Equal(t, kubeapplierapi.ApplyDesireTypeDelete, stale.Spec.Type,
		"stale desires should still be reaped even when HostedCluster is not ready")
}

func TestSyncOnceOverlaysHostedClusterVersion(t *testing.T) {
	t.Parallel()
	releaseImage := "quay.io/openshift-release-dev/ocp-release@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	controlPlaneImage := "quay.io/openshift-release-dev/ocp-release-priv@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	decodeHostedClusterContent := func(t *testing.T, desire *kubeapplierapi.ApplyDesire) map[string]any {
		t.Helper()
		require.NotNil(t, desire.Spec.ServerSideApply, "ApplyDesire must have ServerSideApply")
		require.NotNil(t, desire.Spec.ServerSideApply.KubeContent, "ApplyDesire must have KubeContent")
		var obj map[string]any
		require.NoError(t, json.Unmarshal(desire.Spec.ServerSideApply.KubeContent.Raw, &obj))
		return obj
	}

	newSyncer := func(spc *coreapi.ServiceProviderCluster, resources map[string]string) (*clusterResourcesController, cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire]) {
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		cluster := newCluster()
		mockKubeApplierClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		mockClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
		mockClients.Register(testManagementClusterResourceID, mockKubeApplierClient)
		crud, err := mockKubeApplierClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)

		ctrl := gomock.NewController(t)
		csClient := ocm.NewMockClusterServiceClientSpec(ctrl)
		csClient.EXPECT().GetClusterResources(gomock.Any(), gomock.Any()).Return(buildClusterResources(resources), nil)

		syncer := &clusterResourcesController{
			clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}},
			serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{spc}},
			clustersServiceClient:        csClient,
			kubeApplierDBClients:         mockClients,
			applyDesireLister: &kubeapplierlistertesting.DBApplyDesireLister{
				Clients: mockClients,
				Lister: &fleetlistertesting.SliceManagementClusterLister{
					ManagementClusters: []*fleetapi.ManagementCluster{
						{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: testManagementClusterResourceID}},
					},
				},
			},
			readDesireLister: &kubeapplierlistertesting.DBReadDesireLister{
				Clients: mockClients,
				Lister: &fleetlistertesting.SliceManagementClusterLister{
					ManagementClusters: []*fleetapi.ManagementCluster{
						{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: testManagementClusterResourceID}},
					},
				},
			},
			resourcesCache: lru.New(resourcesCacheCapacity),
			clock:          utilsclock.RealClock{},
		}
		// Create requires ctx but we reuse the same test ctx for SyncOnce
		_ = ctx
		return syncer, crud
	}

	baseHostedClusterJSON := `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"HostedCluster","metadata":{"name":"test-cluster","namespace":"ns"},"spec":{"release":{"image":"quay.io/openshift-release-dev/ocp-release@sha256:old"}}}`

	t.Run("in-band SyncOnce writes release image and ForceUpgradeTo and clears controlPlaneRelease", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		spc := newSPC(testManagementClusterResourceID)
		spc.Spec.DesiredHostedCluster.Spec.Release.Image = releaseImage
		spc.Spec.DesiredHostedCluster.Spec.ControlPlaneRelease = nil

		syncer, crud := newSyncer(spc, map[string]string{"hosted": baseHostedClusterJSON})
		require.NoError(t, syncer.SyncOnce(ctx, testKey()))

		desire, err := crud.Get(ctx, DesireNameHostedCluster)
		require.NoError(t, err)
		assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, desire.Spec.Type)
		obj := decodeHostedClusterContent(t, desire)
		img, _, _ := unstructured.NestedString(obj, "spec", "release", "image")
		assert.Equal(t, releaseImage, img)
		_, hasCP, _ := unstructured.NestedString(obj, "spec", "controlPlaneRelease", "image")
		assert.False(t, hasCP, "in-band SyncOnce must not set controlPlaneRelease")
		ann, _, _ := unstructured.NestedString(obj, "metadata", "annotations", hsv1beta1.ForceUpgradeToAnnotation)
		assert.Equal(t, releaseImage, ann)
	})

	t.Run("out-of-band SyncOnce writes both release and controlPlaneRelease and ForceUpgradeTo to control-plane image", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		spc := newSPC(testManagementClusterResourceID)
		spc.Spec.DesiredHostedCluster.Spec.Release.Image = releaseImage
		spc.Spec.DesiredHostedCluster.Spec.ControlPlaneRelease = &hsv1beta1.Release{Image: controlPlaneImage}
		spc.Spec.DesiredHostedCluster.SetAnnotations(map[string]string{hsv1beta1.ForceUpgradeToAnnotation: controlPlaneImage})

		syncer, crud := newSyncer(spc, map[string]string{"hosted": baseHostedClusterJSON})
		require.NoError(t, syncer.SyncOnce(ctx, testKey()))

		desire, err := crud.Get(ctx, DesireNameHostedCluster)
		require.NoError(t, err)
		obj := decodeHostedClusterContent(t, desire)
		img, _, _ := unstructured.NestedString(obj, "spec", "release", "image")
		assert.Equal(t, releaseImage, img)
		cpImg, _, _ := unstructured.NestedString(obj, "spec", "controlPlaneRelease", "image")
		assert.Equal(t, controlPlaneImage, cpImg)
		ann, _, _ := unstructured.NestedString(obj, "metadata", "annotations", hsv1beta1.ForceUpgradeToAnnotation)
		assert.Equal(t, controlPlaneImage, ann)
	})

	t.Run("recovery to in-band via SyncOnce clears previous out-of-band controlPlaneRelease", func(t *testing.T) {
		t.Parallel()
		ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
		// CS still returns HostedCluster with stale out-of-band state; SPC has moved to in-band
		hostedWithCP := `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"HostedCluster","metadata":{"name":"test-cluster","namespace":"ns","annotations":{"hypershift.openshift.io/force-upgrade-to":"` + controlPlaneImage + `"}},"spec":{"release":{"image":"quay.io/openshift-release-dev/ocp-release@sha256:old"},"controlPlaneRelease":{"image":"` + controlPlaneImage + `"}}}`

		spc := newSPC(testManagementClusterResourceID)
		spc.Spec.DesiredHostedCluster.Spec.Release.Image = releaseImage
		spc.Spec.DesiredHostedCluster.Spec.ControlPlaneRelease = nil

		syncer, crud := newSyncer(spc, map[string]string{"hosted": hostedWithCP})
		require.NoError(t, syncer.SyncOnce(ctx, testKey()))

		desire, err := crud.Get(ctx, DesireNameHostedCluster)
		require.NoError(t, err)
		obj := decodeHostedClusterContent(t, desire)
		_, hasCP, _ := unstructured.NestedString(obj, "spec", "controlPlaneRelease", "image")
		assert.False(t, hasCP, "recovery SyncOnce must clear controlPlaneRelease")
		img, _, _ := unstructured.NestedString(obj, "spec", "release", "image")
		assert.Equal(t, releaseImage, img)
		ann, _, _ := unstructured.NestedString(obj, "metadata", "annotations", hsv1beta1.ForceUpgradeToAnnotation)
		assert.Equal(t, releaseImage, ann)
	})
}

func TestOverlayDesiredHostedClusterVersion(t *testing.T) {
	releaseImage := "quay.io/openshift-release-dev/ocp-release@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	controlPlaneImage := "quay.io/openshift-release-dev/ocp-release-priv@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	newSPC := func(desired *hsv1beta1.HostedCluster) *coreapi.ServiceProviderCluster {
		v := semver.MustParse("4.19.15")
		return &coreapi.ServiceProviderCluster{
			Spec: coreapi.ServiceProviderClusterSpec{
				DesiredHostedCluster: desired,
				ControlPlaneVersion: coreapi.ServiceProviderClusterSpecVersion{
					DesiredVersion: &v,
				},
			},
		}
	}

	newHostedCluster := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "hypershift.openshift.io/v1beta1",
				"kind":       "HostedCluster",
				"metadata": map[string]interface{}{
					"name":      "test-cluster",
					"namespace": "test-ns",
				},
				"spec": map[string]interface{}{
					"release": map[string]interface{}{
						"image": "quay.io/openshift-release-dev/ocp-release@sha256:old",
					},
				},
			},
		}
	}

	c := &clusterResourcesController{}
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())

	t.Run("not ready when DesiredHostedCluster is nil", func(t *testing.T) {
		hc := newHostedCluster()
		origImg, _, _ := unstructured.NestedString(hc.Object, "spec", "release", "image")
		ready, err := c.overlayDesiredHostedClusterVersion(ctx, hc, newSPC(nil))
		require.NoError(t, err)
		assert.False(t, ready, "nil DesiredHostedCluster must be not-ready")
		img, _, _ := unstructured.NestedString(hc.Object, "spec", "release", "image")
		assert.Equal(t, origImg, img, "not-ready must not mutate release image")
	})

	t.Run("not ready when DesiredHostedCluster has empty release image", func(t *testing.T) {
		hc := newHostedCluster()
		origImg, _, _ := unstructured.NestedString(hc.Object, "spec", "release", "image")
		desired := &hsv1beta1.HostedCluster{
			Spec: hsv1beta1.HostedClusterSpec{
				Release: hsv1beta1.Release{Image: ""},
			},
		}
		ready, err := c.overlayDesiredHostedClusterVersion(ctx, hc, newSPC(desired))
		require.NoError(t, err)
		assert.False(t, ready, "empty release image must be not-ready")
		img, _, _ := unstructured.NestedString(hc.Object, "spec", "release", "image")
		assert.Equal(t, origImg, img, "not-ready must not mutate release image")
	})

	t.Run("not ready when DesiredVersion is nil", func(t *testing.T) {
		hc := newHostedCluster()
		origImg, _, _ := unstructured.NestedString(hc.Object, "spec", "release", "image")
		desired := &hsv1beta1.HostedCluster{
			Spec: hsv1beta1.HostedClusterSpec{
				Release: hsv1beta1.Release{Image: releaseImage},
			},
		}
		spc := newSPC(desired)
		spc.Spec.ControlPlaneVersion.DesiredVersion = nil
		ready, err := c.overlayDesiredHostedClusterVersion(ctx, hc, spc)
		require.NoError(t, err)
		assert.False(t, ready, "nil DesiredVersion must be not-ready")
		img, _, _ := unstructured.NestedString(hc.Object, "spec", "release", "image")
		assert.Equal(t, origImg, img, "not-ready must not mutate release image")
	})

	t.Run("not ready when release image empty even with ControlPlaneRelease set", func(t *testing.T) {
		hc := newHostedCluster()
		origImg, _, _ := unstructured.NestedString(hc.Object, "spec", "release", "image")
		desired := &hsv1beta1.HostedCluster{
			Spec: hsv1beta1.HostedClusterSpec{
				Release:             hsv1beta1.Release{Image: ""},
				ControlPlaneRelease: &hsv1beta1.Release{Image: controlPlaneImage},
			},
		}
		ready, err := c.overlayDesiredHostedClusterVersion(ctx, hc, newSPC(desired))
		require.NoError(t, err)
		assert.False(t, ready, "empty release image must be not-ready even with ControlPlaneRelease")
		img, _, _ := unstructured.NestedString(hc.Object, "spec", "release", "image")
		assert.Equal(t, origImg, img, "not-ready must not mutate release image")
	})

	t.Run("sets release image and force-upgrade-to in-band", func(t *testing.T) {
		hc := newHostedCluster()
		desired := &hsv1beta1.HostedCluster{}
		desired.Spec.Release.Image = releaseImage
		desired.Spec.ControlPlaneRelease = nil

		ready, err := c.overlayDesiredHostedClusterVersion(ctx, hc, newSPC(desired))
		require.NoError(t, err)
		assert.True(t, ready)

		img, _, _ := unstructured.NestedString(hc.Object, "spec", "release", "image")
		assert.Equal(t, releaseImage, img)
		_, hasCP, _ := unstructured.NestedString(hc.Object, "spec", "controlPlaneRelease", "image")
		assert.False(t, hasCP, "in-band must not set controlPlaneRelease")
		assert.Equal(t, releaseImage, hc.GetAnnotations()[hsv1beta1.ForceUpgradeToAnnotation],
			"in-band must force-upgrade to releaseImage")
	})

	t.Run("sets controlPlaneRelease and force-upgrade-to out-of-band", func(t *testing.T) {
		hc := newHostedCluster()
		desired := &hsv1beta1.HostedCluster{}
		desired.Spec.Release.Image = releaseImage
		desired.Spec.ControlPlaneRelease = &hsv1beta1.Release{Image: controlPlaneImage}
		desired.SetAnnotations(map[string]string{hsv1beta1.ForceUpgradeToAnnotation: controlPlaneImage})

		ready, err := c.overlayDesiredHostedClusterVersion(ctx, hc, newSPC(desired))
		require.NoError(t, err)
		assert.True(t, ready)

		img, _, _ := unstructured.NestedString(hc.Object, "spec", "release", "image")
		assert.Equal(t, releaseImage, img)
		cpImg, _, _ := unstructured.NestedString(hc.Object, "spec", "controlPlaneRelease", "image")
		assert.Equal(t, controlPlaneImage, cpImg)
		assert.Equal(t, controlPlaneImage, hc.GetAnnotations()[hsv1beta1.ForceUpgradeToAnnotation],
			"out-of-band must force-upgrade to controlPlaneRelease")
	})

	t.Run("preserves unrelated annotations", func(t *testing.T) {
		hc := newHostedCluster()
		hc.SetAnnotations(map[string]string{"example.com/keep": "yes"})
		desired := &hsv1beta1.HostedCluster{}
		desired.Spec.Release.Image = releaseImage

		ready, err := c.overlayDesiredHostedClusterVersion(ctx, hc, newSPC(desired))
		require.NoError(t, err)
		assert.True(t, ready)
		assert.Equal(t, "yes", hc.GetAnnotations()["example.com/keep"])
		assert.Equal(t, releaseImage, hc.GetAnnotations()[hsv1beta1.ForceUpgradeToAnnotation])
	})

	t.Run("clears previous out-of-band state on recovery to in-band", func(t *testing.T) {
		hc := newHostedCluster()
		hc.Object["spec"].(map[string]interface{})["controlPlaneRelease"] = map[string]interface{}{
			"image": controlPlaneImage,
		}
		hc.SetAnnotations(map[string]string{hsv1beta1.ForceUpgradeToAnnotation: controlPlaneImage})

		desired := &hsv1beta1.HostedCluster{}
		desired.Spec.Release.Image = releaseImage
		desired.Spec.ControlPlaneRelease = nil
		desired.SetAnnotations(map[string]string{hsv1beta1.ForceUpgradeToAnnotation: releaseImage})

		ready, err := c.overlayDesiredHostedClusterVersion(ctx, hc, newSPC(desired))
		require.NoError(t, err)
		assert.True(t, ready)

		_, hasCP, _ := unstructured.NestedString(hc.Object, "spec", "controlPlaneRelease", "image")
		assert.False(t, hasCP, "recovery must clear controlPlaneRelease")
		assert.Equal(t, releaseImage, hc.GetAnnotations()[hsv1beta1.ForceUpgradeToAnnotation],
			"recovery must force-upgrade to new releaseImage")
		assert.Equal(t, releaseImage, func() string {
			s, _, _ := unstructured.NestedString(hc.Object, "spec", "release", "image")
			return s
		}())
	})

	t.Run("treats empty ControlPlaneRelease image as in-band", func(t *testing.T) {
		hc := newHostedCluster()
		hc.Object["spec"].(map[string]interface{})["controlPlaneRelease"] = map[string]interface{}{
			"image": controlPlaneImage,
		}
		hc.SetAnnotations(map[string]string{hsv1beta1.ForceUpgradeToAnnotation: controlPlaneImage})
		desired := &hsv1beta1.HostedCluster{}
		desired.Spec.Release.Image = releaseImage
		desired.Spec.ControlPlaneRelease = &hsv1beta1.Release{Image: ""}

		ready, err := c.overlayDesiredHostedClusterVersion(ctx, hc, newSPC(desired))
		require.NoError(t, err)
		assert.True(t, ready)
		_, hasCP, _ := unstructured.NestedString(hc.Object, "spec", "controlPlaneRelease", "image")
		assert.False(t, hasCP, "empty ControlPlaneRelease must be treated as in-band and cleared")
		assert.Equal(t, releaseImage, hc.GetAnnotations()[hsv1beta1.ForceUpgradeToAnnotation])
		img, _, _ := unstructured.NestedString(hc.Object, "spec", "release", "image")
		assert.Equal(t, releaseImage, img)
	})

	t.Run("logs when not ready", func(t *testing.T) {
		hc := newHostedCluster()
		ready, err := c.overlayDesiredHostedClusterVersion(ctx, hc, newSPC(nil))
		require.NoError(t, err)
		assert.False(t, ready)
		// Log assertion is best-effort; the method must not error.
	})

	t.Run("round-trips through JSON", func(t *testing.T) {
		hc := newHostedCluster()
		desired := &hsv1beta1.HostedCluster{}
		desired.Spec.Release.Image = releaseImage
		desired.Spec.ControlPlaneRelease = &hsv1beta1.Release{Image: controlPlaneImage}
		desired.SetAnnotations(map[string]string{hsv1beta1.ForceUpgradeToAnnotation: controlPlaneImage})

		ready, err := c.overlayDesiredHostedClusterVersion(ctx, hc, newSPC(desired))
		require.NoError(t, err)
		assert.True(t, ready)

		raw, err := json.Marshal(hc.Object)
		require.NoError(t, err)
		var restored map[string]interface{}
		require.NoError(t, json.Unmarshal(raw, &restored))
		hc2 := &unstructured.Unstructured{Object: restored}
		assert.Equal(t, releaseImage, func() string { s, _, _ := unstructured.NestedString(hc2.Object, "spec", "release", "image"); return s }())
		assert.Equal(t, controlPlaneImage, func() string {
			s, _, _ := unstructured.NestedString(hc2.Object, "spec", "controlPlaneRelease", "image")
			return s
		}())
	})
}
