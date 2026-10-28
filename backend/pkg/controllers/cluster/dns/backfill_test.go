// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dns

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
)

type readDesireError struct {
	kubeapplierlisters.ReadDesireLister
	err error
}

func (l readDesireError) GetForCluster(context.Context, string, string, string, string) (*kubeapplierapi.ReadDesire, error) {
	return nil, l.err
}

func TestBackfillSyncOnce(t *testing.T) {
	cluster := testCluster("", "legacy")
	cluster.ServiceProviderProperties.ClusterServiceID = cluster.ServiceProviderProperties.PendingClusterServiceID
	cluster.ServiceProviderProperties.PendingClusterServiceID = nil
	established := cluster.DeepCopy()
	established.ServiceProviderProperties.DNS.BaseDomain = "bcdf2.example.com"
	hosted := &hsv1beta1.HostedCluster{Spec: hsv1beta1.HostedClusterSpec{KubeAPIServerDNSName: "api.legacy.bcdf2.example.com", DNS: hsv1beta1.DNSSpec{BaseDomain: "aro.legacy.bcdf2.example.com"}}}
	wrongAPI := hosted.DeepCopy()
	wrongAPI.Spec.KubeAPIServerDNSName = "api.wrong.bcdf2.example.com"
	wrongIngress := hosted.DeepCopy()
	wrongIngress.Spec.DNS.BaseDomain = "wrong.example.com"
	wrongDomain := established.DeepCopy()
	wrongDomain.ServiceProviderProperties.DNS.BaseDomain = "different.example.com"
	other := testClaim()
	other.ClusterUUID = "other"
	retiring := testClaim()
	stamp := metav1.NewTime(testNow)
	retiring.DeletionTimestamp = &stamp
	deleting := cluster.DeepCopy()
	deleting.ServiceProviderProperties.DeletionTimestamp = &stamp
	noPrefix := cluster.DeepCopy()
	noPrefix.CustomerProperties.DNS.BaseDomainPrefix = ""
	failure := errors.New("injected failure")
	preconditionFailed := corecosmosstoragetesting.NewPreconditionFailedError()
	tests := []struct {
		name                        string
		cluster                     *coreapi.Cluster
		hosted                      *hsv1beta1.HostedCluster
		claim                       *fleetapi.DNSClaim
		claimErr, readErr, writeErr error
		wantCluster                 *coreapi.Cluster
		wantClaim                   *fleetapi.DNSClaim
		wantErr                     string
	}{
		{name: "persist observed name", cluster: cluster, hosted: hosted, wantCluster: established},
		{name: "create reservation with fresh timestamp", cluster: established, hosted: hosted, wantClaim: testClaim()},
		{name: "owned reservation", cluster: established, hosted: hosted, claim: testClaim()},
		{name: "API mismatch", cluster: established, hosted: wrongAPI, wantErr: "DNS mismatch"},
		{name: "ingress mismatch", cluster: established, hosted: wrongIngress, wantErr: "DNS mismatch"},
		{name: "committed mismatch", cluster: wrongDomain, hosted: hosted, wantErr: "DNS mismatch"},
		{name: "owner mismatch", cluster: established, hosted: hosted, claim: other, wantErr: "expected"},
		{name: "retiring reservation", cluster: established, hosted: hosted, claim: retiring, wantErr: "unavailable"},
		{name: "claim read failure", cluster: established, hosted: hosted, claimErr: failure, wantErr: failure.Error()},
		{name: "HostedCluster read failure", cluster: established, readErr: failure, wantErr: failure.Error()},
		{name: "replace precondition failed", cluster: cluster, hosted: hosted, writeErr: preconditionFailed, wantCluster: established},
		{name: "create precondition failed", cluster: established, hosted: hosted, writeErr: preconditionFailed, wantClaim: testClaim()},
		{name: "claim already created", cluster: established, hosted: hosted, writeErr: &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "Conflict"}, wantClaim: testClaim()},
		{name: "replace failure", cluster: cluster, hosted: hosted, writeErr: failure, wantCluster: established, wantErr: failure.Error()},
		{name: "create failure", cluster: established, hosted: hosted, writeErr: failure, wantClaim: testClaim(), wantErr: failure.Error()},
		{name: "no HostedCluster", cluster: cluster},
		{name: "no confirmed CS ID", cluster: testCluster("", "legacy"), readErr: failure},
		{name: "deleting", cluster: deleting, readErr: failure},
		{name: "prefix recovery pending", cluster: noPrefix, readErr: failure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources := newRecordingResources()
			resources.clusters.writeErr = tt.writeErr
			fleet := &recordingFleet{}
			fleet.claims.writeErr = tt.writeErr
			desires := &kubeapplierlistertesting.SliceReadDesireLister{}
			if tt.hosted != nil {
				raw, err := json.Marshal(tt.hosted)
				require.NoError(t, err)
				id := metadataapi.Must(azcorearm.ParseResourceID(kubeapplierapihelpers.ToClusterScopedReadDesireResourceIDString("sub", "rg", "test", kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster)))
				desires.Desires = []*kubeapplierapi.ReadDesire{{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id}, Status: kubeapplierapi.ReadDesireStatus{KubeContent: &runtime.RawExtension{Raw: raw}}}}
			}
			var reads kubeapplierlisters.ReadDesireLister = desires
			if tt.readErr != nil {
				reads = readDesireError{err: tt.readErr}
			}
			controller := &backfillController{resourcesDBClient: resources, fleetDBClient: fleet, clusterLister: &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{tt.cluster}}, dnsClaimLister: claimLister(t, tt.claim, tt.claimErr), readDesireLister: reads, clock: clocktesting.NewFakePassiveClock(testNow)}
			beforeCluster, beforeClaim := tt.cluster.DeepCopy(), tt.claim.DeepCopy()
			err := controller.SyncOnce(testContext(t), testKey)
			if len(tt.wantErr) > 0 {
				require.ErrorContains(t, err, tt.wantErr)
				report := &coreapi.Controller{}
				controllerutils.ReportSyncError(err)(report)
				require.Equal(t, metav1.ConditionTrue, meta.FindStatusCondition(report.Status.Conditions, "Degraded").Status)
			} else {
				require.NoError(t, err)
			}
			var wantClusters []*coreapi.Cluster
			if tt.wantCluster != nil {
				wantClusters = []*coreapi.Cluster{tt.wantCluster}
			}
			compare(t, wantClusters, resources.clusters.replacements)
			var wantClaims []*fleetapi.DNSClaim
			if tt.wantClaim != nil {
				wantClaims = []*fleetapi.DNSClaim{tt.wantClaim}
			}
			compare(t, wantClaims, fleet.claims.creates)
			require.Empty(t, resources.providers.replacements)
			require.Empty(t, fleet.claims.replacements)
			require.Empty(t, fleet.claims.deletes)
			compare(t, beforeCluster, tt.cluster)
			compare(t, beforeClaim, tt.claim)
		})
	}
}
