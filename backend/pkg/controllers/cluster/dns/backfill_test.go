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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
)

func TestDecideBackfill(t *testing.T) {
	now := time.Unix(10000, 0)
	domain := "abcd.example.com"
	cluster := testCluster(coreapi.ServiceProviderDNSProfile{}, "legacy")
	csID := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/unrelated-cs-id"))
	cluster.ServiceProviderProperties.ClusterServiceID = &csID
	established := cluster.DeepCopy()
	established.ServiceProviderProperties.DNS.BaseDomain = domain
	id := metadataapi.Must(fleetapihelpers.DNSClaimResourceID(domain))
	wantClaim := &fleetapi.DNSClaim{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: "example.com"}, BaseDomain: domain, Owner: "aro-uid", ClusterResourceID: cluster.ID.String(), CreatedAt: metav1.NewTime(now)}
	hc := &hsv1beta1.HostedCluster{Spec: hsv1beta1.HostedClusterSpec{KubeAPIServerDNSName: "api.legacy." + domain, DNS: hsv1beta1.DNSSpec{BaseDomain: "aro.legacy." + domain}}}
	wrongAPI := hc.DeepCopy()
	wrongAPI.Spec.KubeAPIServerDNSName = "api.wrong." + domain
	wrongIngress := hc.DeepCopy()
	wrongIngress.Spec.DNS.BaseDomain = "wrong.example.com"
	wrongDomain := established.DeepCopy()
	wrongDomain.ServiceProviderProperties.DNS.BaseDomain = "different.example.com"
	otherClaim := wantClaim.DeepCopy()
	otherClaim.Owner = "other-uid"
	failure := errors.New("read failure")
	tests := []struct {
		name      string
		cluster   *coreapi.Cluster
		hc        *hsv1beta1.HostedCluster
		claim     *fleetapi.DNSClaim
		readErr   error
		want      decision
		wantError string
	}{
		{name: "persist legacy name", cluster: cluster, hc: hc, want: decision{Action: writeCluster, Cluster: established}},
		{name: "fresh claim timestamp for old cluster", cluster: established, hc: hc, want: decision{Action: createClaim, Claim: wantClaim}},
		{name: "existing own claim", cluster: established, hc: hc, claim: wantClaim, want: decision{Action: noWrite}},
		{name: "API mismatch degraded", cluster: established, hc: wrongAPI, want: decision{Action: noWrite}, wantError: "DNS mismatch"},
		{name: "ingress mismatch degraded", cluster: established, hc: wrongIngress, want: decision{Action: noWrite}, wantError: "DNS mismatch"},
		{name: "base domain mismatch degraded", cluster: wrongDomain, hc: hc, want: decision{Action: noWrite}, wantError: "DNS mismatch"},
		{name: "claim owner mismatch", cluster: established, hc: hc, claim: otherClaim, want: decision{Action: noWrite}, wantError: "expected"},
		{name: "observation error", cluster: established, hc: hc, readErr: failure, want: decision{Action: noWrite, Err: failure}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.cluster.DeepCopy()
			got := decideBackfill(logr.Discard(), tt.cluster, tt.hc, nil, now, func(string) (*fleetapi.DNSClaim, error) { return tt.claim, tt.readErr })
			got.validate()
			if tt.wantError != "" {
				if got.Err == nil || !strings.Contains(got.Err.Error(), tt.wantError) {
					t.Fatalf("expected %s got %v", tt.wantError, got.Err)
				}
				report := &coreapi.Controller{}
				controllerutils.ReportSyncError(got.Err)(report)
				condition := meta.FindStatusCondition(report.Status.Conditions, "Degraded")
				if condition == nil || condition.Status != metav1.ConditionTrue {
					t.Fatal("mismatch must report controller degradation")
				}
				got.Err = nil
			}
			if diff := cmp.Diff(tt.want, got, comparison...); diff != "" {
				t.Fatal(diff)
			}
			if diff := cmp.Diff(before, tt.cluster, comparison...); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestBackfillEligibilityIgnoresIrrelevantObservationError(t *testing.T) {
	cluster := testCluster(coreapi.ServiceProviderDNSProfile{}, "legacy")
	got := decideBackfill(logr.Discard(), cluster, nil, errors.New("read unavailable"), time.Time{}, nil)
	if diff := cmp.Diff(decision{Action: noWrite}, got); diff != "" {
		t.Fatal(diff)
	}
}
