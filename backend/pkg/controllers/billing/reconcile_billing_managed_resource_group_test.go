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

package billing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/go-logr/logr/testr"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/billingcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Record patch requests while the shared billing fake enforces patch conditions.
type managedRGPatchClient struct {
	billingcosmosstorage.BillingDBClient
	patches     []json.RawMessage
	patchErr    error
	beforePatch func()
}

func (c *managedRGPatchClient) BillingDocs(subscriptionID string) billingcosmosstorage.BillingDocCRUD {
	return &managedRGPatchCRUD{BillingDocCRUD: c.BillingDBClient.BillingDocs(subscriptionID), client: c}
}

type managedRGPatchCRUD struct {
	billingcosmosstorage.BillingDocCRUD
	client *managedRGPatchClient
}

func (c *managedRGPatchCRUD) GetByID(ctx context.Context, id string) (*billingcosmosstorage.BillingDocument, error) {
	return nil, errors.New("unexpected live billing read")
}

func (c *managedRGPatchCRUD) PatchByID(ctx context.Context, id string, ops billingcosmosstorage.BillingDocumentPatchOperations) error {
	data, err := json.Marshal(ops)
	if err != nil {
		return err
	}
	c.client.patches = append(c.client.patches, data)
	if c.client.patchErr != nil {
		return c.client.patchErr
	}
	if c.client.beforePatch != nil {
		c.client.beforePatch()
	}
	return c.BillingDocCRUD.PatchByID(ctx, id, ops)
}

type unexpectedBillingLookup struct {
	corelisters.BillingLister
	t *testing.T
}

func (l unexpectedBillingLookup) GetByID(context.Context, string) (*billingcosmosstorage.BillingDocument, error) {
	l.t.Fatal("invalid cluster billing link must be rejected before looking up the document")
	return nil, nil
}

func TestReconcileBillingManagedResourceGroup(t *testing.T) {
	created := mustParseTime("2025-01-15T10:30:00Z")
	desired := coreapihelpers.ToResourceGroupResourceIDString(testSubscriptionID, "managed-rg")
	for _, tt := range []struct {
		name                         string
		changeCluster                func(*coreapi.Cluster)
		changeDoc                    func(*billingcosmosstorage.BillingDocument)
		missingCluster, missingCache bool
		patchErr                     error
		wantErr                      string
		wantReason                   string
		wantPatches                  int
		wantRG                       string
		forbidBillingLookup          bool
	}{
		{name: "repairs customer RG", wantPatches: 1, wantRG: desired},
		{name: "repairs missing RG", changeDoc: func(d *billingcosmosstorage.BillingDocument) { d.ManagedResourceGroup = "" }, wantPatches: 1, wantRG: desired},
		{name: "converged", changeDoc: func(d *billingcosmosstorage.BillingDocument) { d.ManagedResourceGroup = desired }},
		{name: "noncanonical RG is corrected", changeDoc: func(d *billingcosmosstorage.BillingDocument) { d.ManagedResourceGroup = strings.ToUpper(desired) + "/" }, wantPatches: 1, wantRG: desired},
		{name: "unset configured RG clears stale billing RG", changeCluster: func(c *coreapi.Cluster) { c.CustomerProperties.Platform.ManagedResourceGroup = "" }, wantPatches: 1, wantRG: ""},
		{name: "unset RG already matches", changeCluster: func(c *coreapi.Cluster) { c.CustomerProperties.Platform.ManagedResourceGroup = "" }, changeDoc: func(d *billingcosmosstorage.BillingDocument) { d.ManagedResourceGroup = "" }},
		{name: "unlinked cluster", changeCluster: func(c *coreapi.Cluster) { c.ServiceProviderProperties.BillingDocumentCosmosID = "" }},
		{name: "missing UID", changeCluster: func(c *coreapi.Cluster) { c.ServiceProviderProperties.ClusterUID = "" }, wantReason: "ClusterIdentityMismatch", forbidBillingLookup: true},
		{name: "UID disagrees with billing link", changeCluster: func(c *coreapi.Cluster) { c.ServiceProviderProperties.ClusterUID = "new-incarnation" }, wantReason: "ClusterIdentityMismatch", forbidBillingLookup: true},
		{name: "wrong resource", changeDoc: func(d *billingcosmosstorage.BillingDocument) {
			d.ResourceID = metadataapi.Must(azcorearm.ParseResourceID(newTestClusterResourceID(t).String() + "-other"))
		}, wantReason: "ClusterIdentityMismatch"},
		{name: "missing resource ID", changeDoc: func(d *billingcosmosstorage.BillingDocument) { d.ResourceID = nil }, wantReason: "ClusterIdentityMismatch"},
		{name: "repair uses cluster subscription", changeDoc: func(d *billingcosmosstorage.BillingDocument) { d.SubscriptionID = "another-subscription" }, wantPatches: 1, wantRG: desired},
		{name: "deleted billing", changeDoc: func(d *billingcosmosstorage.BillingDocument) { d.DeletionTime = &created }},
		{name: "deleting cluster", changeCluster: func(c *coreapi.Cluster) { c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: created} }},
		{name: "legacy deleting cluster", changeCluster: func(c *coreapi.Cluster) {
			c.ServiceProviderProperties.ProvisioningState = coreapi.ProvisioningStateDeleting
		}},
		{name: "cluster cache miss", missingCluster: true},
		{name: "billing cache miss waits for informer", missingCache: true},
		{name: "patch failure", patchErr: errors.New("patch failed"), wantErr: "patch failed", wantPatches: 1, wantRG: desired},
		{name: "concurrent billing deletion", patchErr: &azcore.ResponseError{StatusCode: http.StatusPreconditionFailed}, wantPatches: 1, wantRG: desired},
		{name: "concurrent document removal", patchErr: &azcore.ResponseError{StatusCode: http.StatusNotFound}, wantPatches: 1, wantRG: desired},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var reasons []string
			logger := funcr.NewJSON(func(line string) {
				var entry map[string]any
				require.NoError(t, json.Unmarshal([]byte(line), &entry))
				if entry["event"] == "billing_metadata_inconsistency" {
					reasons = append(reasons, entry["reason"].(string))
				}
			}, funcr.Options{})
			ctx := utils.ContextWithLogger(t.Context(), logger)
			cluster := newTestCluster(t, testClusterUID, coreapi.ProvisioningStateSucceeded, &created)
			cluster.ServiceProviderProperties.BillingDocumentCosmosID = testClusterUID
			cluster.CustomerProperties.Platform.ManagedResourceGroup = "managed-rg"
			if tt.changeCluster != nil {
				tt.changeCluster(cluster)
			}
			billingClient := billingcosmosstoragetesting.NewMockBillingDBClient()
			doc := billingcosmosstorage.NewBillingDocument(testClusterUID, newTestClusterResourceID(t))
			doc.CreationTime = created
			doc.ManagedResourceGroup = coreapihelpers.ToResourceGroupResourceIDString(testSubscriptionID, testResourceGroupName)
			require.NoError(t, billingClient.BillingDocs(testSubscriptionID).Create(ctx, doc))
			cachedDoc := doc.DeepCopy()
			if tt.changeDoc != nil {
				tt.changeDoc(cachedDoc)
			}
			var cachedDocs []*billingcosmosstorage.BillingDocument
			if !tt.missingCache {
				cachedDocs = append(cachedDocs, cachedDoc)
			}
			client := &managedRGPatchClient{BillingDBClient: billingClient, patchErr: tt.patchErr}
			var cachedClusters []*coreapi.Cluster
			if !tt.missingCluster {
				cachedClusters = append(cachedClusters, cluster)
			}
			billingLister := &corelistertesting.SliceBillingLister{BillingDocuments: cachedDocs}
			var lookup corelisters.BillingLister = billingLister
			if tt.forbidBillingLookup {
				lookup = unexpectedBillingLookup{t: t}
			}
			syncer := newReconcileBillingManagedResourceGroupSyncer(client,
				&corelistertesting.SliceClusterLister{Clusters: cachedClusters}, lookup)
			err := syncer.SyncOnce(ctx, newTestClusterKey())
			if cosmosstorageutils.IsPreconditionFailedError(tt.patchErr) {
				require.ErrorIs(t, err, tt.patchErr)
			} else if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			if tt.wantReason != "" {
				if diff := cmp.Diff([]string{tt.wantReason}, reasons); diff != "" {
					t.Fatalf("diagnostics (-want +got):\n%s", diff)
				}
			} else {
				require.Empty(t, reasons)
			}
			require.Len(t, client.patches, tt.wantPatches)
			for _, patch := range client.patches {
				// A single-field PATCH preserves Billing-owned watermarks and unknown fields.
				require.JSONEq(t, `{"condition":"FROM c WHERE NOT IS_DEFINED(c.deletionTime) OR IS_NULL(c.deletionTime)","operations":[{"op":"set","path":"/managedResourceGroup","value":"`+tt.wantRG+`"}]}`, string(patch))
			}
			if tt.wantPatches > 0 && tt.patchErr == nil {
				persisted := billingClient.GetBillingDocuments()[doc.ID]
				if diff := cmp.Diff(tt.wantRG, persisted.ManagedResourceGroup); diff != "" {
					t.Fatalf("managed RG (-want +got):\n%s", diff)
				}
				billingLister.BillingDocuments = []*billingcosmosstorage.BillingDocument{persisted.DeepCopy()}
				require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()))
				require.Len(t, client.patches, 1)
			}
			if tt.missingCache {
				billingLister.BillingDocuments = []*billingcosmosstorage.BillingDocument{doc.DeepCopy()}
				require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()))
				require.Len(t, client.patches, 1, "repair resumes when the informer discovers the document")
				require.Equal(t, desired, billingClient.GetBillingDocuments()[doc.ID].ManagedResourceGroup)
			}
		})
	}
}

func TestManagedRGPatchPreservesOtherFieldsAndSkipsClosedRecords(t *testing.T) {
	for _, tc := range []struct {
		name       string
		change     func(*billingcosmosstorage.BillingDocument)
		wantRepair bool
	}{
		{name: "unrelated update preserved", change: func(d *billingcosmosstorage.BillingDocument) { d.Location = "westus3" }, wantRepair: true},
		{name: "billing watermark preserved", change: func(d *billingcosmosstorage.BillingDocument) {
			d.LastBillingTimeOfMngtEvent = ptr.To(mustParseTime("2026-10-01T00:00:00Z"))
		}, wantRepair: true},
		{name: "concurrent deletion stops repair", change: func(d *billingcosmosstorage.BillingDocument) {
			d.DeletionTime = ptr.To(mustParseTime("2026-10-01T00:00:00Z"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			created := mustParseTime("2025-01-15T10:30:00Z")
			cluster := newTestCluster(t, testClusterUID, coreapi.ProvisioningStateSucceeded, &created)
			cluster.ServiceProviderProperties.BillingDocumentCosmosID = testClusterUID
			cluster.CustomerProperties.Platform.ManagedResourceGroup = "managed-rg"
			doc := billingcosmosstorage.NewBillingDocument(testClusterUID, cluster.ID)
			doc.CreationTime = created
			doc.ManagedResourceGroup = "customer-rg"
			billingDB := billingcosmosstoragetesting.NewMockBillingDBClient()
			require.NoError(t, billingDB.BillingDocs(testSubscriptionID).Create(ctx, doc))
			stored := billingDB.GetBillingDocuments()[doc.ID]
			client := &managedRGPatchClient{BillingDBClient: billingDB, beforePatch: func() {
				tc.change(stored)
			}}
			billingLister := &corelistertesting.SliceBillingLister{BillingDocuments: []*billingcosmosstorage.BillingDocument{doc.DeepCopy()}}
			syncer := newReconcileBillingManagedResourceGroupSyncer(client,
				&corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster.DeepCopy()}}, billingLister)
			err := syncer.SyncOnce(ctx, newTestClusterKey())
			if tc.wantRepair {
				require.NoError(t, err)
			} else {
				require.True(t, cosmosstorageutils.IsPreconditionFailedError(err), "%v", err)
			}
			want := stored.DeepCopy()
			if tc.wantRepair {
				want.ManagedResourceGroup = coreapihelpers.ToResourceGroupResourceIDString(testSubscriptionID, "managed-rg")
			}
			require.Len(t, client.patches, 1)
			persisted := billingDB.GetBillingDocuments()[doc.ID]
			want.CosmosETag = persisted.CosmosETag
			if diff := cmp.Diff(want, persisted, cmp.Comparer(func(a, b *azcorearm.ResourceID) bool {
				if a == nil || b == nil {
					return a == b
				}
				return a.String() == b.String()
			})); diff != "" {
				t.Fatalf("persisted document (-want +got):\n%s", diff)
			}
			client.beforePatch = nil
			billingLister.BillingDocuments = []*billingcosmosstorage.BillingDocument{persisted.DeepCopy()}
			require.NoError(t, syncer.SyncOnce(ctx, newTestClusterKey()))
			require.Len(t, client.patches, 1)
		})
	}
}

type managedRGNotifier struct {
	handler cache.ResourceEventHandler
	options cache.HandlerOptions
	err     error
}

func (n *managedRGNotifier) AddEventHandlerWithOptions(handler cache.ResourceEventHandler, options cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	n.handler = handler
	n.options = options
	return nil, n.err
}

type managedRGRecordingSyncer struct {
	controllerutil.GenericSyncer[controllerutils.HCPClusterKey]
	keys chan controllerutils.HCPClusterKey
}

func (s *managedRGRecordingSyncer) SyncOnce(_ context.Context, key controllerutils.HCPClusterKey) error {
	s.keys <- key
	return nil
}

func TestManagedRGWatchRouting(t *testing.T) {
	for _, tc := range []struct {
		name      string
		notify    func(*managedRGNotifier, *managedRGNotifier, *coreapi.Cluster, *billingcosmosstorage.BillingDocument)
		wantNames []string
	}{
		{name: "cluster add", notify: func(c, b *managedRGNotifier, cluster *coreapi.Cluster, doc *billingcosmosstorage.BillingDocument) {
			c.handler.OnAdd(cluster, true)
		}, wantNames: []string{testClusterName}},
		{name: "cluster resync", notify: func(c, b *managedRGNotifier, cluster *coreapi.Cluster, doc *billingcosmosstorage.BillingDocument) {
			c.handler.OnUpdate(cluster, cluster)
		}, wantNames: []string{testClusterName}},
		{name: "billing add maps ARM ID rather than document UUID", notify: func(c, b *managedRGNotifier, cluster *coreapi.Cluster, doc *billingcosmosstorage.BillingDocument) {
			b.handler.OnAdd(doc, true)
		}, wantNames: []string{testClusterName}},
		{name: "billing resync queues current cluster", notify: func(c, b *managedRGNotifier, cluster *coreapi.Cluster, doc *billingcosmosstorage.BillingDocument) {
			b.handler.OnUpdate(doc, doc)
		}, wantNames: []string{testClusterName}},
		{name: "billing resource ID change queues current cluster only", notify: func(c, b *managedRGNotifier, cluster *coreapi.Cluster, doc *billingcosmosstorage.BillingDocument) {
			other := billingcosmosstorage.NewBillingDocument(doc.ID, metadataapi.Must(azcorearm.ParseResourceID(cluster.ID.String()+"-other")))
			b.handler.OnUpdate(doc, other)
		}, wantNames: []string{testClusterName + "-other"}},
		{name: "missing billing resource ID", notify: func(c, b *managedRGNotifier, cluster *coreapi.Cluster, doc *billingcosmosstorage.BillingDocument) {
			b.handler.OnAdd(&billingcosmosstorage.BillingDocument{}, true)
		}},
		{name: "wrong resource type", notify: func(c, b *managedRGNotifier, cluster *coreapi.Cluster, doc *billingcosmosstorage.BillingDocument) {
			b.handler.OnAdd(billingcosmosstorage.NewBillingDocument(doc.ID, metadataapi.Must(azcorearm.ParseResourceID(cluster.ID.String()+"/nodePools/np"))), true)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(utils.ContextWithLogger(t.Context(), testr.New(t)))
				defer cancel()
				clusterNotifier, billingNotifier := &managedRGNotifier{}, &managedRGNotifier{}
				syncer := &managedRGRecordingSyncer{GenericSyncer: newReconcileBillingManagedResourceGroupSyncer(nil, nil, nil), keys: make(chan controllerutils.HCPClusterKey, 10)}
				controller, err := newManagedRGWatchingController(syncer, clusterNotifier, billingNotifier)
				require.NoError(t, err)
				require.Equal(t, time.Hour, *clusterNotifier.options.ResyncPeriod)
				require.Equal(t, time.Hour, *billingNotifier.options.ResyncPeriod)
				cluster := newTestCluster(t, testClusterUID, coreapi.ProvisioningStateSucceeded, nil)
				doc := billingcosmosstorage.NewBillingDocument(testClusterUID, cluster.ID)
				tc.notify(clusterNotifier, billingNotifier, cluster, doc)
				go controller.Run(ctx, 1)
				synctest.Wait()
				time.Sleep(time.Second)
				synctest.Wait()
				cancel()
				synctest.Wait()
				close(syncer.keys)
				var gotNames []string
				for key := range syncer.keys {
					require.Equal(t, testSubscriptionID, key.SubscriptionID)
					require.Equal(t, testResourceGroupName, key.ResourceGroupName)
					gotNames = append(gotNames, key.HCPClusterName)
				}
				if diff := cmp.Diff(tc.wantNames, gotNames); diff != "" {
					t.Fatalf("queued clusters (-want +got):\n%s", diff)
				}
			})
		})
	}
}

func TestManagedRGWatchRegistrationErrors(t *testing.T) {
	failure := errors.New("watch registration failed")
	for _, tc := range []struct {
		name                   string
		clusterErr, billingErr error
	}{
		{name: "cluster", clusterErr: failure}, {name: "billing", billingErr: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			controller, err := newManagedRGWatchingController(newReconcileBillingManagedResourceGroupSyncer(nil, nil, nil), &managedRGNotifier{err: tc.clusterErr}, &managedRGNotifier{err: tc.billingErr})
			require.ErrorIs(t, err, failure)
			require.Nil(t, controller)
		})
	}
}
