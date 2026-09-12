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

package operations

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/lru"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationClusterUpdate_SynchronizeOperation(t *testing.T) {
	testClockNow := operationtesting.MustParseTime("2024-06-01T12:00:00Z")
	fixture := operationtesting.NewClusterTestFixture()

	newClusterWithCustomerVersion := func(versionID string, mutate ...func(*coreapi.HCPOpenShiftCluster)) *coreapi.HCPOpenShiftCluster {
		cluster := fixture.NewCluster(nil)
		cluster.CustomerProperties.Version.ID = versionID
		for _, fn := range mutate {
			if fn != nil {
				fn(cluster)
			}
		}
		return cluster
	}

	newCSClusterWithState := func(state arohcpv1alpha1.ClusterState) *arohcpv1alpha1.Cluster {
		allowAccess := arohcpv1alpha1.NewCIDRBlockAllowAccess().Mode(ocm.CSCIDRBlockAllowAccessModeAllowAll)
		csCluster, err := arohcpv1alpha1.NewCluster().
			API(arohcpv1alpha1.NewClusterAPI().
				CIDRBlockAccess(arohcpv1alpha1.NewCIDRBlockAccess().
					Allow(allowAccess))).
			Status(arohcpv1alpha1.NewClusterStatus().State(state)).
			Build()
		require.NoError(t, err)
		return csCluster
	}

	newCSClusterReadyWithNodeDrainMinutes := func(minutes int32) *arohcpv1alpha1.Cluster {
		allowAccess := arohcpv1alpha1.NewCIDRBlockAllowAccess().Mode(ocm.CSCIDRBlockAllowAccessModeAllowAll)
		csCluster, err := arohcpv1alpha1.NewCluster().
			NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
				Unit("minutes").
				Value(float64(minutes))).
			API(arohcpv1alpha1.NewClusterAPI().
				CIDRBlockAccess(arohcpv1alpha1.NewCIDRBlockAccess().
					Allow(allowAccess))).
			Status(arohcpv1alpha1.NewClusterStatus().State(arohcpv1alpha1.ClusterStateReady)).
			Build()
		require.NoError(t, err)
		return csCluster
	}

	newOperationAccepted := func() *coreapi.Operation {
		return fixture.NewOperation(cosmosstorageutils.OperationRequestUpdate)
	}

	newServiceProviderClusterWithSpecControlPlaneVersion := func(version string) *coreapi.ServiceProviderCluster {
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s",
			fixture.ClusterResourceID.String(),
			coreapi.ServiceProviderClusterResourceTypeName,
			coreapi.ServiceProviderClusterResourceName,
		)))
		parsedVersion, err := semver.ParseTolerant(version)
		require.NoError(t, err)
		activeVersions := []coreapi.ServiceProviderClusterActiveVersion{{Version: ptr.To(parsedVersion)}}
		return &coreapi.ServiceProviderCluster{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
			Spec: coreapi.ServiceProviderClusterSpec{
				ControlPlaneVersion: coreapi.ServiceProviderClusterSpecVersion{
					DesiredVersion: ptr.To(parsedVersion),
				},
			},
			Status: coreapi.ServiceProviderClusterStatus{
				ControlPlaneVersion: coreapi.ServiceProviderClusterStatusVersion{
					ActiveVersions: activeVersions,
				},
			},
		}
	}
	newDefaultControlPlaneDesiredVersionController := func() *coreapi.Controller {
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(fixture.ClusterResourceID.String() + "/hcpOpenShiftControllers/ControlPlaneDesiredVersion"))
		return &coreapi.Controller{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
			ExternalID:     fixture.ClusterResourceID,
			Status:         coreapi.ControllerStatus{},
		}
	}

	newControlPlaneDesiredVersionControllerWithConditions := func(conditions []metav1.Condition) *coreapi.Controller {
		controller := newDefaultControlPlaneDesiredVersionController()
		controller.Status.Conditions = conditions
		return controller
	}

	newPassingCachedHostedClusterReadDesire := func() *kubeapplierapi.ReadDesire {
		return operationtesting.NewHostedClusterReadDesire(t, &v1beta1.HostedCluster{
			Spec: operationtesting.ClusterUpdateMatchingHostedClusterSpec(),
		})
	}

	testCases := []struct {
		name            string
		existingCluster *coreapi.HCPOpenShiftCluster
		// When not set, the controller uses a cluster lister that contains the existingCluster
		clusterLister     corelisters.ClusterLister
		existingOperation *coreapi.Operation
		// When not set, the controller uses an active operations lister that contains the existingOperation
		activeOperationsLister         corelisters.ActiveOperationLister
		existingServiceProviderCluster *coreapi.ServiceProviderCluster
		// When not set, the controller uses a service provider cluster lister that contains the existingServiceProviderCluster
		serviceProviderClusterLister                 corelisters.ServiceProviderClusterLister
		existingControlPlaneDesiredVersionController *coreapi.Controller
		// When set, wires a ReadDesireLister containing this cached HostedCluster mirror.
		cachedHostedClusterReadDesire                 *kubeapplierapi.ReadDesire
		cachedControlPlaneClusterAutoscalerReadDesire *kubeapplierapi.ReadDesire
		seedMismatchFirstSeenAt                       time.Time
		setupMockCSClient                             func(*ocm.MockClusterServiceClientSpec)
		wantErr                                       bool
		verifyDB                                      func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient)
	}{
		{
			name:                           "cs cluster ready transitions operation to succeeded",
			existingCluster:                newClusterWithCustomerVersion("4.19"),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),

			cachedHostedClusterReadDesire: newPassingCachedHostedClusterReadDesire(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.ClusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateReady), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateSucceeded, op.Status)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateSucceeded, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Empty(t, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:                           "cs cluster updating transitions operation to updating",
			existingCluster:                newClusterWithCustomerVersion("4.19"),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			cachedHostedClusterReadDesire:  newPassingCachedHostedClusterReadDesire(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.ClusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateUpdating), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateUpdating, op.Status)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateUpdating, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Equal(t, operationtesting.TestOperationName, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:                           "cs cluster error transitions operation to failed",
			existingCluster:                newClusterWithCustomerVersion("4.19"),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			cachedHostedClusterReadDesire:  newPassingCachedHostedClusterReadDesire(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.ClusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateError), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateFailed, op.Status)
				assert.NotNil(t, op.Error)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateFailed, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Empty(t, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:                           "cs cluster pending keeps operation accepted",
			existingCluster:                newClusterWithCustomerVersion("4.19"),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			cachedHostedClusterReadDesire:  newPassingCachedHostedClusterReadDesire(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.ClusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStatePending), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:                           "customer minor mismatch with IntentFailed on ControlPlaneDesiredVersion controller marks operation failed",
			existingCluster:                newClusterWithCustomerVersion("4.20"),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			existingControlPlaneDesiredVersionController: newControlPlaneDesiredVersionControllerWithConditions([]metav1.Condition{
				{
					Type:    coreapi.ControllerConditionTypeIntentFailed,
					Status:  metav1.ConditionTrue,
					Reason:  coreapi.VersionUpgradeNotAcceptedReason,
					Message: "example intent failed message",
				},
			}),
			cachedHostedClusterReadDesire: newPassingCachedHostedClusterReadDesire(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.ClusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateReady), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateFailed, op.Status)
				require.NotNil(t, op.Error)
				assert.Equal(t, coreapi.CloudErrorCodeInvalidRequestContent, op.Error.Code)
				assert.Contains(t, op.Error.Message, "example intent failed message")

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateFailed, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Empty(t, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:                           "customer minor mismatch without ControlPlaneDesiredVersion IntentFailed leaves operation accepted",
			existingCluster:                newClusterWithCustomerVersion("4.20"),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			cachedHostedClusterReadDesire:  newPassingCachedHostedClusterReadDesire(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.ClusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateReady), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)
				assert.Nil(t, op.Error)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.Equal(t, operationtesting.TestOperationName, cluster.ServiceProviderProperties.ActiveOperationID)
				assert.Empty(t, cluster.ServiceProviderProperties.ProvisioningState)
			},
		},
		{
			name:                           "customer minor mismatch without ControlPlaneDesiredVersion IntentFailed leaves operation accepted when first seen within 129s",
			existingCluster:                newClusterWithCustomerVersion("4.20"),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			cachedHostedClusterReadDesire:  newPassingCachedHostedClusterReadDesire(),
			seedMismatchFirstSeenAt:        testClockNow.Add(-120 * time.Second),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.ClusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateReady), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)
				assert.Nil(t, op.Error)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.Equal(t, operationtesting.TestOperationName, cluster.ServiceProviderProperties.ActiveOperationID)
				assert.Empty(t, cluster.ServiceProviderProperties.ProvisioningState)
			},
		},
		{
			name:                           "customer minor mismatch without IntentFailed fails when mismatch first seen exceeds 129s",
			existingCluster:                newClusterWithCustomerVersion("4.20"),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			cachedHostedClusterReadDesire:  newPassingCachedHostedClusterReadDesire(),
			seedMismatchFirstSeenAt:        testClockNow.Add(-130 * time.Second),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.ClusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateReady), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateFailed, op.Status)
				require.NotNil(t, op.Error)
				assert.Equal(t, coreapi.CloudErrorCodeInvalidRequestContent, op.Error.Code)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				wantMessageSubstr := fmt.Sprintf(
					"timed out after 129s waiting for resolution of desired version from '%s' cluster version",
					cluster.CustomerProperties.Version.ID,
				)
				assert.Contains(t, op.Error.Message, wantMessageSubstr)

				assert.Equal(t, coreapi.ProvisioningStateFailed, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Empty(t, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name: "shouldReconcile gate not passed when ClusterServiceID is nil",
			existingCluster: newClusterWithCustomerVersion("4.19", func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.ServiceProviderProperties.ClusterServiceID = nil
			}),
			existingOperation: newOperationAccepted(),
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name: "shouldReconcile gate not passed when cluster is deleting",
			existingCluster: newClusterWithCustomerVersion("4.19", func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: testClockNow}
			}),
			existingOperation: newOperationAccepted(),
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:              "cluster not in lister cache leaves operation unchanged",
			existingCluster:   newClusterWithCustomerVersion("4.19"),
			existingOperation: newOperationAccepted(),
			clusterLister:     &corelistertesting.SliceClusterLister{},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.Equal(t, operationtesting.TestOperationName, cluster.ServiceProviderProperties.ActiveOperationID)
				assert.Empty(t, cluster.ServiceProviderProperties.ProvisioningState)
			},
		},
		{
			name: "cs cluster ready with node drain spec mismatch keeps operation updating",
			existingCluster: newClusterWithCustomerVersion("4.19", func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.CustomerProperties.NodeDrainTimeoutMinutes = 60
			}),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			cachedHostedClusterReadDesire:  newPassingCachedHostedClusterReadDesire(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.ClusterInternalID).
					Return(newCSClusterReadyWithNodeDrainMinutes(30), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateUpdating, op.Status)
				assert.Nil(t, op.Error)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateUpdating, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Equal(t, operationtesting.TestOperationName, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name: "cs cluster ready with customerManaged KMS etcd key version match transitions operation to succeeded",
			existingCluster: newClusterWithCustomerVersion("4.19", func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.CustomerProperties.Etcd = coreapi.EtcdProfile{
					DataEncryption: coreapi.EtcdDataEncryptionProfile{
						KeyManagementMode: metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
						CustomerManaged: &coreapi.CustomerManagedEncryptionProfile{
							EncryptionType: metadataapi.CustomerManagedEncryptionTypeKMS,
							Kms: &coreapi.KmsEncryptionProfile{
								Visibility: metadataapi.KeyVaultVisibilityPublic,
								ActiveKey: coreapi.KmsKey{
									Name:      "test-key",
									VaultName: "test-vault",
									Version:   "v1",
								},
							},
						},
					},
				}
			}),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			cachedHostedClusterReadDesire: operationtesting.NewHostedClusterReadDesire(t, &v1beta1.HostedCluster{
				Spec: func() v1beta1.HostedClusterSpec {
					spec := operationtesting.ClusterUpdateMatchingHostedClusterSpec()
					spec.SecretEncryption = &v1beta1.SecretEncryptionSpec{
						Type: v1beta1.KMS,
						KMS: &v1beta1.KMSSpec{
							Azure: &v1beta1.AzureKMSSpec{
								ActiveKey: v1beta1.AzureKMSKey{
									KeyVersion: "v1",
								},
							},
						},
					}
					return spec
				}(),
			}),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.ClusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateReady), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name: "cs cluster ready with CMK KMS etcd key version mismatch keeps operation updating",
			existingCluster: newClusterWithCustomerVersion("4.19", func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.CustomerProperties.Etcd = coreapi.EtcdProfile{
					DataEncryption: coreapi.EtcdDataEncryptionProfile{
						KeyManagementMode: metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
						CustomerManaged: &coreapi.CustomerManagedEncryptionProfile{
							EncryptionType: metadataapi.CustomerManagedEncryptionTypeKMS,
							Kms: &coreapi.KmsEncryptionProfile{
								Visibility: metadataapi.KeyVaultVisibilityPublic,
								ActiveKey: coreapi.KmsKey{
									Name:      "test-key",
									VaultName: "test-vault",
									Version:   "v2",
								},
							},
						},
					},
				}
			}),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			cachedHostedClusterReadDesire: operationtesting.NewHostedClusterReadDesire(t, &v1beta1.HostedCluster{
				Spec: func() v1beta1.HostedClusterSpec {
					spec := operationtesting.ClusterUpdateMatchingHostedClusterSpec()
					spec.SecretEncryption = &v1beta1.SecretEncryptionSpec{
						Type: v1beta1.KMS,
						KMS: &v1beta1.KMSSpec{
							Azure: &v1beta1.AzureKMSSpec{
								ActiveKey: v1beta1.AzureKMSKey{
									KeyVersion: "v1",
								},
							},
						},
					}
					return spec
				}(),
			}),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.ClusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateReady), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateUpdating, op.Status)
				assert.Nil(t, op.Error)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateUpdating, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Equal(t, operationtesting.TestOperationName, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name: "cs cluster ready with hypershift autoscaling spec mismatch keeps operation updating",
			existingCluster: newClusterWithCustomerVersion("4.19", func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.CustomerProperties.Autoscaling.MaxNodesTotal = 10
			}),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			cachedHostedClusterReadDesire: operationtesting.NewHostedClusterReadDesire(t, &v1beta1.HostedCluster{
				Spec: func() v1beta1.HostedClusterSpec {
					spec := operationtesting.ClusterUpdateMatchingHostedClusterSpec()
					spec.Autoscaling.MaxNodesTotal = ptr.To[int32](5)
					return spec
				}(),
			}),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.ClusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateReady), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateUpdating, op.Status)
				assert.Nil(t, op.Error)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateUpdating, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Equal(t, operationtesting.TestOperationName, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:                           "autoscaler all checks pass but autoscaler ReadDesire not cached yet keeps operation updating",
			existingCluster:                newClusterWithCustomerVersion("4.20"),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.20"),
			cachedHostedClusterReadDesire:  newPassingCachedHostedClusterReadDesire(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.ClusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateReady), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateUpdating, op.Status)
				assert.Nil(t, op.Error)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateUpdating, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Equal(t, operationtesting.TestOperationName, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:                           "autoscaler all checks pass, operation transitions to succeeded",
			existingCluster:                newClusterWithCustomerVersion("4.20"),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.20"),
			cachedHostedClusterReadDesire:  newPassingCachedHostedClusterReadDesire(),
			cachedControlPlaneClusterAutoscalerReadDesire: newControlPlaneClusterAutoscalerReadDesire(t, readyControlPlaneClusterAutoscaler()),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.ClusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateReady), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateSucceeded, op.Status)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateSucceeded, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Empty(t, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)

			resources := []any{}
			if tc.existingCluster != nil {
				resources = append(resources, tc.existingCluster)
			}
			if tc.existingOperation != nil {
				resources = append(resources, tc.existingOperation)
			}
			if tc.existingServiceProviderCluster != nil {
				resources = append(resources, tc.existingServiceProviderCluster)
			}
			if tc.existingControlPlaneDesiredVersionController != nil {
				resources = append(resources, tc.existingControlPlaneDesiredVersionController)
			}

			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			var readDesires []*kubeapplierapi.ReadDesire
			if tc.cachedHostedClusterReadDesire != nil {
				readDesires = append(readDesires, tc.cachedHostedClusterReadDesire)
			}
			if tc.cachedControlPlaneClusterAutoscalerReadDesire != nil {
				readDesires = append(readDesires, tc.cachedControlPlaneClusterAutoscalerReadDesire)
			}

			clusterLister := tc.clusterLister
			if clusterLister == nil {
				clusterLister = &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDBClient}
			}
			activeOperationsLister := tc.activeOperationsLister
			if activeOperationsLister == nil {
				activeOperationsLister = &corelistertesting.DBActiveOperationLister{ResourcesDBClient: mockResourcesDBClient}
			}
			serviceProviderClusterLister := tc.serviceProviderClusterLister
			if serviceProviderClusterLister == nil {
				serviceProviderClusterLister = &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDBClient}
			}

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.setupMockCSClient != nil {
				tc.setupMockCSClient(mockCSClient)
			}

			fakeClock := clocktesting.NewFakeClock(testClockNow)

			controller := &operationClusterUpdate{
				resourcesDBClient:               mockResourcesDBClient,
				clusterServiceClient:            mockCSClient,
				clusterLister:                   clusterLister,
				activeOperationsLister:          activeOperationsLister,
				serviceProviderClusterLister:    serviceProviderClusterLister,
				readDesireLister:                &kubeapplierlistertesting.SliceReadDesireLister{Desires: readDesires},
				notificationClient:              nil,
				clock:                           fakeClock,
				desiredVersionMismatchFirstSeen: lru.New(100000),
			}
			if !tc.seedMismatchFirstSeenAt.IsZero() {
				require.NotNil(t, tc.existingOperation)
				controller.desiredVersionMismatchFirstSeen.Add(tc.existingOperation.ResourceID.String(), tc.seedMismatchFirstSeenAt)
			}

			err = controller.SynchronizeOperation(ctx, fixture.OperationKey())
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tc.verifyDB != nil {
				tc.verifyDB(t, ctx, mockResourcesDBClient)
			}
		})
	}
}
