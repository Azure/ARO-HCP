// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package credentialrevocation

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
)

func TestDecideReservationCleanup(t *testing.T) {
	id := metadataapi.Must(coreapihelpers.ToClusterResourceID("sub", "rg", "cluster"))
	rid := metadataapi.Must(coreapihelpers.SystemAdminCredentialRevocationID(id, "operation"))
	stamp := metav1.NewTime(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	cluster := func(owner *azcorearm.ResourceID, requestedAt *metav1.Time, legacy string) *coreapi.Cluster {
		return &coreapi.Cluster{
			CosmosMetadata:            coreapi.CosmosMetadata{ResourceID: id, PartitionKey: "sub", CosmosETag: "fresh-etag", InstanceVersion: 7},
			TrackedResource:           coreapi.TrackedResource{Resource: coreapi.Resource{ID: id}},
			ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{ClusterUID: "uid", SystemAdminCredentialRevocationResourceID: owner, SystemAdminCredentialRevocationRequestedAt: requestedAt, RevokeCredentialsOperationID: legacy},
		}
	}
	missing := &azcore.ResponseError{StatusCode: http.StatusNotFound}
	failure := errors.New("read failed")
	intent := &coreapi.SystemAdminCredentialRevocation{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: rid}}
	otherID := metadataapi.Must(coreapihelpers.SystemAdminCredentialRevocationID(id, "other"))
	later := metav1.NewTime(stamp.Add(time.Minute))
	comparison := cmp.Options{cmp.Comparer(func(a, b *azcorearm.ResourceID) bool {
		if a == nil || b == nil {
			return a == b
		}
		return a.String() == b.String()
	}), cmp.Comparer(func(a, b metadataapi.InternalID) bool { return a.String() == b.String() }), cmpopts.EquateErrors()}
	for _, tc := range []struct {
		name  string
		age   time.Duration
		alter func(*reservationObservation)
		want  ownershipDecision
	}{
		{name: "missing before grace", age: 30*time.Minute - time.Nanosecond},
		{name: "missing exactly grace", age: 30 * time.Minute, want: ownershipDecision{Cluster: cluster(nil, nil, "")}},
		{name: "missing after grace", age: 31 * time.Minute, want: ownershipDecision{Cluster: cluster(nil, nil, "")}},
		{name: "cached existing overdue", age: time.Hour, alter: func(o *reservationObservation) { o.CachedRevocation = intent; o.CachedErr = nil; o.LiveErr = failure }},
		{name: "cached miss live found overdue", age: time.Hour, alter: func(o *reservationObservation) { o.LiveRevocation = intent; o.LiveErr = nil }},
		{name: "cache failure", age: time.Hour, alter: func(o *reservationObservation) { o.CachedErr = failure }, want: ownershipDecision{Err: failure}},
		{name: "live failure", age: time.Hour, alter: func(o *reservationObservation) { o.LiveErr = failure }, want: ownershipDecision{Err: failure}},
		{name: "fresh cluster failure", age: time.Hour, alter: func(o *reservationObservation) { o.FreshErr = failure }, want: ownershipDecision{Err: failure}},
		{name: "fresh owner changed", age: time.Hour, alter: func(o *reservationObservation) { o.FreshCluster = cluster(otherID, &stamp, "other") }},
		{name: "fresh timestamp changed", age: time.Hour, alter: func(o *reservationObservation) { o.FreshCluster = cluster(rid, &later, "operation") }},
		{name: "fresh timestamp erased", age: time.Hour, alter: func(o *reservationObservation) { o.FreshCluster = cluster(rid, nil, "operation") }},
		{name: "fresh cluster identity changed", age: time.Hour, alter: func(o *reservationObservation) { o.FreshCluster.ServiceProviderProperties.ClusterUID = "new-uid" }},
		{name: "fresh cluster deleting", age: time.Hour, alter: func(o *reservationObservation) { o.FreshCluster.ServiceProviderProperties.DeletionTimestamp = &stamp }},
		{name: "legacy timestamp absent fail safe", age: time.Hour, alter: func(o *reservationObservation) {
			o.Cluster.ServiceProviderProperties.SystemAdminCredentialRevocationRequestedAt = nil
		}},
		{name: "different legacy sentinel retained", age: time.Hour, alter: func(o *reservationObservation) {
			o.FreshCluster.ServiceProviderProperties.RevokeCredentialsOperationID = "other"
		}, want: ownershipDecision{Cluster: cluster(nil, nil, "other")}},
		{name: "already released no churn", age: time.Hour, alter: func(o *reservationObservation) { o.Cluster = cluster(nil, nil, "") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk := clocktesting.NewFakeClock(stamp.Time)
			clk.Step(tc.age)
			o := reservationObservation{Cluster: cluster(rid, &stamp, "operation"), CachedErr: missing, LiveErr: missing, FreshCluster: cluster(rid, &stamp, "operation")}
			o.Cluster.CosmosETag = "cached-etag"
			if tc.alter != nil {
				tc.alter(&o)
			}
			before, freshBefore := o.Cluster.DeepCopy(), o.FreshCluster.DeepCopy()
			got := decideReservationCleanup(logr.Discard(), o, clk.Now())
			got.validate()
			if diff := cmp.Diff(tc.want, got, comparison...); diff != "" {
				t.Fatal(diff)
			}
			if diff := cmp.Diff(before, o.Cluster, comparison...); diff != "" {
				t.Fatal("mutated cache: " + diff)
			}
			if diff := cmp.Diff(freshBefore, o.FreshCluster, comparison...); diff != "" {
				t.Fatal("mutated live observation: " + diff)
			}
		})
	}
}
