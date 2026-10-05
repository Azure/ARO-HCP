// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package autonode

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// seedStatusTestCluster creates the HCP cluster plus its ServiceProviderCluster
// in the mock DB, mirroring what CreateServiceProviderCluster would have done
// by the time this syncer first runs.
func seedStatusTestCluster(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, opts ...func(*coreapi.Cluster)) {
	t.Helper()

	cluster := newTestCluster(opts...)
	_, err := db.HCPClusters(testSub, testRG).Create(ctx, cluster, nil)
	require.NoError(t, err)

	_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, db, testClusterResourceID())
	require.NoError(t, err)
}

// newAutoNodeReadDesire builds a ReadDesire carrying a marshaled HostedCluster
// whose status.autoNode is the given value.
func newAutoNodeReadDesire(t *testing.T, autoNode hyperv1beta1.AutoNodeStatus) *kubeapplierapi.ReadDesire {
	t.Helper()

	hc := &hyperv1beta1.HostedCluster{}
	hc.APIVersion = "hypershift.openshift.io/v1beta1"
	hc.Kind = "HostedCluster"
	hc.SetName(testClusterName)
	hc.Status.AutoNode = autoNode
	raw, err := json.Marshal(hc)
	require.NoError(t, err)

	rdResourceIDStr := kubeapplierapihelpers.ToClusterScopedReadDesireResourceIDString(
		testSub, testRG, testClusterName, kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster,
	)
	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(rdResourceIDStr)),
			PartitionKey: strings.ToLower(testMgmtClusterResourceID().String()),
		},
		Status: kubeapplierapi.ReadDesireStatus{
			KubeContent: &kruntime.RawExtension{Raw: raw},
		},
	}
}

// newAutoNodeReadDesireWithCondition builds a ReadDesire carrying a marshaled
// HostedCluster whose status.autoNode and AutoNodeEnabled condition are the
// given values, for pinning the "enabled but zero nodes" vs "not enabled"
// distinction that the condition (not the node counts) resolves.
func newAutoNodeReadDesireWithCondition(t *testing.T, autoNode hyperv1beta1.AutoNodeStatus, condition metav1.Condition) *kubeapplierapi.ReadDesire {
	t.Helper()

	hc := &hyperv1beta1.HostedCluster{}
	hc.APIVersion = "hypershift.openshift.io/v1beta1"
	hc.Kind = "HostedCluster"
	hc.SetName(testClusterName)
	hc.Status.AutoNode = autoNode
	hc.Status.Conditions = []metav1.Condition{condition}
	raw, err := json.Marshal(hc)
	require.NoError(t, err)

	rdResourceIDStr := kubeapplierapihelpers.ToClusterScopedReadDesireResourceIDString(
		testSub, testRG, testClusterName, kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster,
	)
	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(rdResourceIDStr)),
			PartitionKey: strings.ToLower(testMgmtClusterResourceID().String()),
		},
		Status: kubeapplierapi.ReadDesireStatus{
			KubeContent: &kruntime.RawExtension{Raw: raw},
		},
	}
}

// newAutoNodeApplyDesire builds the AutoNodeEnabler's ApplyDesire document
// carrying the given status conditions, for pinning how AutoNodeStatus
// surfaces a stuck/rejected delivery.
func newAutoNodeApplyDesire(conditions ...metav1.Condition) *kubeapplierapi.ApplyDesire {
	adResourceIDStr := kubeapplierapihelpers.ToClusterScopedApplyDesireResourceIDString(
		testSub, testRG, testClusterName, autoNodeApplyDesireName,
	)
	return &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(adResourceIDStr)),
			PartitionKey: strings.ToLower(testMgmtClusterResourceID().String()),
		},
		Status: kubeapplierapi.ApplyDesireStatus{
			Conditions: conditions,
		},
	}
}

func newStatusTestSyncer(
	db *corecosmosstoragetesting.MockResourcesDBClient,
	desires []*kubeapplierapi.ReadDesire,
	applyDesires ...*kubeapplierapi.ApplyDesire,
) *autoNodeStatusSyncer {
	return &autoNodeStatusSyncer{
		resourcesDBClient:            db,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: db},
		readDesireLister:             &kubeapplierlistertesting.SliceReadDesireLister{Desires: desires},
		applyDesireLister:            &kubeapplierlistertesting.SliceApplyDesireLister{Desires: applyDesires},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: db},
	}
}

func getStoredAutoNode(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) *coreapi.ServiceProviderClusterAutoNodeStatus {
	t.Helper()
	spc, err := db.ServiceProviderClusters(testSub, testRG, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	return spc.Status.AutoNode
}

func TestAutoNodeStatusSyncer_SyncOnce(t *testing.T) {
	tests := []struct {
		name         string
		seed         func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, opts ...func(*coreapi.Cluster))
		desires      func(t *testing.T) []*kubeapplierapi.ReadDesire
		applyDesires func(t *testing.T) []*kubeapplierapi.ApplyDesire
		expected     *coreapi.ServiceProviderClusterAutoNodeStatus
	}{
		{
			name: "cluster not found is a no-op",
			seed: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ ...func(*coreapi.Cluster)) {
			},
		},
		{
			name: "cluster being deleted is a no-op",
			seed: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ ...func(*coreapi.Cluster)) {
				seedStatusTestCluster(t, ctx, db, func(c *coreapi.Cluster) {
					c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				})
			},
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newAutoNodeReadDesire(t, hyperv1beta1.AutoNodeStatus{NodeCount: ptr.To(int32(3))})}
			},
			expected: nil,
		},
		{
			name: "no ReadDesire yet is a no-op",
			seed: seedStatusTestCluster,
			// GetCachedHostedClusterForCluster returns (nil, nil); we wait for
			// kube-applier to observe the HostedCluster.
			expected: nil,
		},
		{
			name: "AutoNode counts are mirrored",
			seed: seedStatusTestCluster,
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newAutoNodeReadDesire(t, hyperv1beta1.AutoNodeStatus{
					NodeCount:      ptr.To(int32(3)),
					NodeClaimCount: ptr.To(int32(5)),
					VCPUs:          ptr.To(int32(24)),
				})}
			},
			expected: &coreapi.ServiceProviderClusterAutoNodeStatus{
				NodeCount:      ptr.To(int32(3)),
				NodeClaimCount: ptr.To(int32(5)),
				VCPUs:          ptr.To(int32(24)),
			},
		},
		{
			name: "zero counts are mirrored, not treated as absent",
			seed: seedStatusTestCluster,
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newAutoNodeReadDesire(t, hyperv1beta1.AutoNodeStatus{
					NodeCount:      ptr.To(int32(0)),
					NodeClaimCount: ptr.To(int32(0)),
					VCPUs:          ptr.To(int32(0)),
				})}
			},
			expected: &coreapi.ServiceProviderClusterAutoNodeStatus{
				NodeCount:      ptr.To(int32(0)),
				NodeClaimCount: ptr.To(int32(0)),
				VCPUs:          ptr.To(int32(0)),
			},
		},
		{
			name: "partially reported AutoNode is mirrored as-is",
			seed: seedStatusTestCluster,
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newAutoNodeReadDesire(t, hyperv1beta1.AutoNodeStatus{
					NodeClaimCount: ptr.To(int32(2)),
				})}
			},
			expected: &coreapi.ServiceProviderClusterAutoNodeStatus{NodeClaimCount: ptr.To(int32(2))},
		},
		{
			name: "empty status.autoNode maps to nil",
			seed: seedStatusTestCluster,
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newAutoNodeReadDesire(t, hyperv1beta1.AutoNodeStatus{})}
			},
			expected: nil,
		},
		{
			name: "AutoNodeEnabled condition True but zero nodes: distinguishable from not-enabled",
			seed: seedStatusTestCluster,
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newAutoNodeReadDesireWithCondition(t,
					hyperv1beta1.AutoNodeStatus{},
					metav1.Condition{
						Type:   string(hyperv1beta1.AutoNodeEnabled),
						Status: metav1.ConditionTrue,
						Reason: "AsExpected",
					},
				)}
			},
			expected: &coreapi.ServiceProviderClusterAutoNodeStatus{
				Enabled: ptr.To(true),
				Condition: &coreapi.ServiceProviderClusterAutoNodeCondition{
					Status: string(metav1.ConditionTrue),
					Reason: "AsExpected",
				},
			},
		},
		{
			name: "AutoNodeEnabled condition False/AutoNodeNotConfigured: distinguishable from never-observed",
			seed: seedStatusTestCluster,
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newAutoNodeReadDesireWithCondition(t,
					hyperv1beta1.AutoNodeStatus{},
					metav1.Condition{
						Type:    string(hyperv1beta1.AutoNodeEnabled),
						Status:  metav1.ConditionFalse,
						Reason:  hyperv1beta1.AutoNodeNotConfiguredReason,
						Message: "AutoNode is not configured",
					},
				)}
			},
			expected: &coreapi.ServiceProviderClusterAutoNodeStatus{
				Enabled: ptr.To(false),
				Condition: &coreapi.ServiceProviderClusterAutoNodeCondition{
					Status:  string(metav1.ConditionFalse),
					Reason:  hyperv1beta1.AutoNodeNotConfiguredReason,
					Message: "AutoNode is not configured",
				},
			},
		},
		{
			name: "disabling AutoNode clears a previously mirrored value",
			seed: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, _ ...func(*coreapi.Cluster)) {
				seedStatusTestCluster(t, ctx, db)
				spcCRUD := db.ServiceProviderClusters(testSub, testRG, testClusterName)
				existing, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				replacement := existing.DeepCopy()
				replacement.Status.AutoNode = &coreapi.ServiceProviderClusterAutoNodeStatus{NodeCount: ptr.To(int32(7))}
				_, err = spcCRUD.Replace(ctx, replacement, nil)
				require.NoError(t, err)
			},
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				// The hypershift-operator zeroes status.autoNode on disable.
				return []*kubeapplierapi.ReadDesire{newAutoNodeReadDesire(t, hyperv1beta1.AutoNodeStatus{})}
			},
			expected: nil,
		},
		{
			name: "rejected ApplyDesire is surfaced even though the HostedCluster mirror is otherwise nil",
			seed: seedStatusTestCluster,
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				// HostedCluster observed and reconciled, but AutoNode was
				// never actually delivered to it (no AutoNodeEnabled
				// condition, no counts) - exactly what you'd see if the SSA
				// itself was rejected before ever reaching the object.
				return []*kubeapplierapi.ReadDesire{newAutoNodeReadDesire(t, hyperv1beta1.AutoNodeStatus{})}
			},
			applyDesires: func(t *testing.T) []*kubeapplierapi.ApplyDesire {
				return []*kubeapplierapi.ApplyDesire{newAutoNodeApplyDesire(metav1.Condition{
					Type:    kubeapplierapi.ConditionTypeSuccessfullyApplied,
					Status:  metav1.ConditionFalse,
					Reason:  kubeapplierapi.ConditionReasonKubeAPIError,
					Message: "HostedCluster.spec.autoNode.provisionerConfig.karpenter.platform: Unsupported value: \"Azure\"",
				})}
			},
			expected: &coreapi.ServiceProviderClusterAutoNodeStatus{
				DeliveryCondition: &coreapi.ServiceProviderClusterAutoNodeCondition{
					Status:  string(metav1.ConditionFalse),
					Reason:  kubeapplierapi.ConditionReasonKubeAPIError,
					Message: "HostedCluster.spec.autoNode.provisionerConfig.karpenter.platform: Unsupported value: \"Azure\"",
				},
			},
		},
		{
			name: "successfully applied ApplyDesire does not populate DeliveryCondition",
			seed: seedStatusTestCluster,
			desires: func(t *testing.T) []*kubeapplierapi.ReadDesire {
				return []*kubeapplierapi.ReadDesire{newAutoNodeReadDesire(t, hyperv1beta1.AutoNodeStatus{
					NodeCount: ptr.To(int32(1)),
				})}
			},
			applyDesires: func(t *testing.T) []*kubeapplierapi.ApplyDesire {
				return []*kubeapplierapi.ApplyDesire{newAutoNodeApplyDesire(metav1.Condition{
					Type:   kubeapplierapi.ConditionTypeSuccessfullyApplied,
					Status: metav1.ConditionTrue,
					Reason: kubeapplierapi.ConditionReasonNoErrors,
				})}
			},
			expected: &coreapi.ServiceProviderClusterAutoNodeStatus{
				NodeCount: ptr.To(int32(1)),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			tt.seed(t, ctx, db)

			var desires []*kubeapplierapi.ReadDesire
			if tt.desires != nil {
				desires = tt.desires(t)
			}
			var applyDesires []*kubeapplierapi.ApplyDesire
			if tt.applyDesires != nil {
				applyDesires = tt.applyDesires(t)
			}

			require.NoError(t, newStatusTestSyncer(db, desires, applyDesires...).SyncOnce(ctx, testKey()))

			if _, err := db.HCPClusters(testSub, testRG).Get(ctx, testClusterName); err != nil {
				// Nothing was seeded; there is no ServiceProviderCluster to assert on.
				return
			}
			assert.Equal(t, tt.expected, getStoredAutoNode(t, ctx, db))
		})
	}
}

// TestAutoNodeStatusSyncer_NoReplaceWhenUnchanged guards the compare in
// SyncOnce. Karpenter node counts churn on every scale event and this syncer
// runs on a 5 minute resync, so without the compare every steady-state cycle
// would issue a Cosmos Replace whose only effect is a new _etag.
func TestAutoNodeStatusSyncer_NoReplaceWhenUnchanged(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	db := corecosmosstoragetesting.NewMockResourcesDBClient()
	seedStatusTestCluster(t, ctx, db)

	desires := []*kubeapplierapi.ReadDesire{newAutoNodeReadDesire(t, hyperv1beta1.AutoNodeStatus{
		NodeCount:      ptr.To(int32(3)),
		NodeClaimCount: ptr.To(int32(3)),
		VCPUs:          ptr.To(int32(24)),
	})}
	syncer := newStatusTestSyncer(db, desires)

	require.NoError(t, syncer.SyncOnce(ctx, testKey()))

	spcCRUD := db.ServiceProviderClusters(testSub, testRG, testClusterName)
	afterFirst, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)

	require.NoError(t, syncer.SyncOnce(ctx, testKey()))

	afterSecond, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	assert.Equal(t, afterFirst.CosmosETag, afterSecond.CosmosETag,
		"ServiceProviderCluster.CosmosETag changed despite an identical AutoNode status; the syncer wrote unnecessarily")
}
