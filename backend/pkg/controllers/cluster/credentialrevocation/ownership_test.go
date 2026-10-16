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
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func TestDecideLegacyReference(t *testing.T) {
	id := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))
	rid := metadataapi.Must(azcorearm.ParseResourceID(id.String() + "/systemAdminCredentialRevocations/intent"))
	otherID := metadataapi.Must(azcorearm.ParseResourceID(id.String() + "/systemAdminCredentialRevocations/other"))
	stamp := metav1.NewTime(time.Unix(100, 0))
	cluster := func(legacy string, owner *azcorearm.ResourceID) *coreapi.Cluster {
		return &coreapi.Cluster{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: "sub", CosmosETag: "observed-etag", InstanceVersion: 3}, TrackedResource: coreapi.TrackedResource{Resource: coreapi.Resource{ID: id}}, ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{RevokeCredentialsOperationID: legacy, SystemAdminCredentialRevocationResourceID: owner}}
	}
	intent := &coreapi.SystemAdminCredentialRevocation{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: rid}, Spec: coreapi.SystemAdminCredentialRevocationSpec{OperationID: "operation"}}
	active := &coreapi.SystemAdminCredentialRevocation{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: otherID}, Spec: coreapi.SystemAdminCredentialRevocationSpec{OperationID: "other"}, Status: coreapi.SystemAdminCredentialRevocationStatus{Conditions: []metav1.Condition{{Type: coreapi.SystemAdminCredentialRevocationConditionActive, Status: metav1.ConditionTrue}}}}
	deleting := intent.DeepCopy()
	deleting.Status.DeletionTimestamp = &stamp
	deletingCluster := cluster("operation", nil)
	deletingCluster.ServiceProviderProperties.DeletionTimestamp = &stamp
	failure := errors.New("observation failed")
	comparison := cmp.Options{cmp.Comparer(func(a, b *azcorearm.ResourceID) bool {
		if a == nil || b == nil {
			return a == b
		}
		return a.String() == b.String()
	}), cmp.Comparer(func(a, b metadataapi.InternalID) bool { return a.String() == b.String() }), cmpopts.EquateErrors()}
	for _, tc := range []struct {
		name    string
		cluster *coreapi.Cluster
		intents []*coreapi.SystemAdminCredentialRevocation
		listErr error
		op      *coreapi.Operation
		opErr   error
		want    ownershipDecision
	}{
		{name: "no sentinel ignores irrelevant errors", cluster: cluster("", nil), listErr: failure, opErr: failure},
		{name: "internal reference belongs to lifecycle", cluster: cluster("operation", rid), listErr: failure},
		{name: "pending legacy operation waits", cluster: cluster("operation", nil), op: &coreapi.Operation{Status: coreapi.ProvisioningStateAccepted}},
		{name: "legacy document migrates with cluster ETag", cluster: cluster("operation", nil), intents: []*coreapi.SystemAdminCredentialRevocation{intent}, want: ownershipDecision{Cluster: cluster("operation", rid)}},
		{name: "timeout does not release internal intent", cluster: cluster("operation", nil), intents: []*coreapi.SystemAdminCredentialRevocation{intent}, op: &coreapi.Operation{Status: coreapi.ProvisioningStateFailed}, want: ownershipDecision{Cluster: cluster("operation", rid)}},
		{name: "active owner wins over pending legacy writer", cluster: cluster("operation", nil), intents: []*coreapi.SystemAdminCredentialRevocation{intent, active}, want: ownershipDecision{Cluster: cluster("other", otherID)}},
		{name: "never reacquire deleting intent", cluster: cluster("operation", nil), intents: []*coreapi.SystemAdminCredentialRevocation{deleting}},
		{name: "never acquire on deleting cluster", cluster: deletingCluster, intents: []*coreapi.SystemAdminCredentialRevocation{intent}},
		{name: "completed legacy operation releases sentinel", cluster: cluster("operation", nil), op: &coreapi.Operation{Status: coreapi.ProvisioningStateSucceeded}, want: ownershipDecision{Cluster: cluster("", nil)}},
		{name: "expired before dispatch releases sentinel", cluster: cluster("operation", nil), op: &coreapi.Operation{Status: coreapi.ProvisioningStateFailed}, want: ownershipDecision{Cluster: cluster("", nil)}},
		{name: "linked missing document releases sentinel", cluster: cluster("operation", nil), op: &coreapi.Operation{Status: coreapi.ProvisioningStateDeleting, SystemAdminCredentialRevocation: &coreapi.OperationSystemAdminCredentialRevocation{SystemAdminCredentialRevocationResourceID: rid}}, want: ownershipDecision{Cluster: cluster("", nil)}},
		{name: "operation past retention", cluster: cluster("operation", nil), want: ownershipDecision{Cluster: cluster("", nil)}},
		{name: "list error", cluster: cluster("operation", nil), listErr: failure, want: ownershipDecision{Err: failure}},
		{name: "operation error", cluster: cluster("operation", nil), opErr: failure, want: ownershipDecision{Err: failure}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.cluster.DeepCopy()
			got := decideLegacyReference(logr.Discard(), tc.cluster, tc.intents, tc.listErr, tc.op, tc.opErr)
			got.validate()
			if diff := cmp.Diff(tc.want, got, comparison...); diff != "" {
				t.Fatal(diff)
			}
			if diff := cmp.Diff(before, tc.cluster, comparison...); diff != "" {
				t.Fatal("mutated observation: " + diff)
			}
		})
	}
}

func TestOwnershipDecisionValidation(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected invalid mutation/error decision to panic")
		}
	}()
	ownershipDecision{Cluster: &coreapi.Cluster{}, Err: errors.New("failure")}.validate()
}

func TestDecideClaim(t *testing.T) {
	id := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))
	rid := metadataapi.Must(azcorearm.ParseResourceID(id.String() + "/systemAdminCredentialRevocations/intent"))
	otherID := metadataapi.Must(azcorearm.ParseResourceID(id.String() + "/systemAdminCredentialRevocations/other"))
	cluster := func(legacy string, owner *azcorearm.ResourceID) *coreapi.Cluster {
		return &coreapi.Cluster{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: "sub", CosmosETag: "claim-etag"}, TrackedResource: coreapi.TrackedResource{Resource: coreapi.Resource{ID: id}}, ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{RevokeCredentialsOperationID: legacy, SystemAdminCredentialRevocationResourceID: owner}}
	}
	intent := &coreapi.SystemAdminCredentialRevocation{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: rid}, Spec: coreapi.SystemAdminCredentialRevocationSpec{OperationID: "operation"}}
	pending := &coreapi.SystemAdminCredentialRevocation{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: otherID}, Spec: coreapi.SystemAdminCredentialRevocationSpec{OperationID: "other"}}
	active := pending.DeepCopy()
	active.Status.Conditions = []metav1.Condition{{Type: coreapi.SystemAdminCredentialRevocationConditionActive, Status: metav1.ConditionTrue}}
	stamp := metav1.NewTime(time.Unix(100, 0))
	deleting := intent.DeepCopy()
	deleting.Status.DeletionTimestamp = &stamp
	deletingCluster := cluster("", nil)
	deletingCluster.ServiceProviderProperties.DeletionTimestamp = &stamp
	comparison := cmp.Options{cmp.Comparer(func(a, b *azcorearm.ResourceID) bool {
		if a == nil || b == nil {
			return a == b
		}
		return a.String() == b.String()
	}), cmp.Comparer(func(a, b metadataapi.InternalID) bool { return a.String() == b.String() })}
	for _, tc := range []struct {
		name    string
		cluster *coreapi.Cluster
		intent  *coreapi.SystemAdminCredentialRevocation
		others  []*coreapi.SystemAdminCredentialRevocation
		want    ownershipDecision
	}{
		{name: "unowned claim with ETag", cluster: cluster("", nil), intent: intent, want: ownershipDecision{Cluster: cluster("operation", rid)}},
		{name: "pending competitors do not deadlock", cluster: cluster("", nil), intent: intent, others: []*coreapi.SystemAdminCredentialRevocation{pending}, want: ownershipDecision{Cluster: cluster("operation", rid)}},
		{name: "other active owner blocks after field erasure", cluster: cluster("", nil), intent: intent, others: []*coreapi.SystemAdminCredentialRevocation{active}},
		{name: "own legacy sentinel", cluster: cluster("operation", nil), intent: intent, want: ownershipDecision{Cluster: cluster("operation", rid)}},
		{name: "other legacy sentinel waits", cluster: cluster("other", nil), intent: intent},
		{name: "own reference resumes after interrupted status", cluster: cluster("operation", rid), intent: intent, want: ownershipDecision{Owned: true}},
		{name: "other reference waits", cluster: cluster("other", otherID), intent: intent},
		{name: "released deleting intent cannot reacquire", cluster: cluster("", nil), intent: deleting},
		{name: "deleting intent cannot act even while referenced", cluster: cluster("operation", rid), intent: deleting},
		{name: "cluster deletion stops claims", cluster: deletingCluster, intent: intent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.cluster.DeepCopy()
			got := decideClaim(logr.Discard(), tc.cluster, tc.intent, tc.others)
			got.validate()
			if diff := cmp.Diff(tc.want, got, comparison...); diff != "" {
				t.Fatal(diff)
			}
			if diff := cmp.Diff(before, tc.cluster, comparison...); diff != "" {
				t.Fatal("mutated observation: " + diff)
			}
		})
	}
}
