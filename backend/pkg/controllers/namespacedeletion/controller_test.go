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

package namespacedeletion

import (
	"testing"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
)

func TestWaitForHostedClusterRemoval(t *testing.T) {
	deleted := metav1.Now()
	observation := hostedClusterObservation{
		present:           true,
		name:              "hcp1",
		deletionTimestamp: &deleted,
	}
	syncer := &namespaceDeletionSyncer{config: Config{HostedClusterRemovalTimeout: time.Hour}}

	issued := metav1.Now()
	request := &fleetapi.NamespaceDeletion{}
	request.Status.HostedClusterDeleteIssuedAt = &issued
	if err := syncer.waitForHostedClusterRemoval(request, observation); err != nil {
		t.Fatal(err)
	}
	if apimeta.IsStatusConditionTrue(request.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionStopped) {
		t.Fatal("must keep waiting after HostedCluster gains a deletionTimestamp")
	}
	if apimeta.IsStatusConditionTrue(request.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionComplete) {
		t.Fatal("must not complete while HostedCluster is still present")
	}

	expired := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	request.Status.HostedClusterDeleteIssuedAt = &expired
	if err := syncer.waitForHostedClusterRemoval(request, observation); err != nil {
		t.Fatal(err)
	}
	if !apimeta.IsStatusConditionTrue(request.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionStopped) {
		t.Fatal("must stop when HostedCluster removal times out")
	}
}

func TestWaitForAlreadyDeletingHostedCluster(t *testing.T) {
	deleted := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	observation := hostedClusterObservation{
		present:           true,
		name:              "hcp1",
		deletionTimestamp: &deleted,
	}
	request := &fleetapi.NamespaceDeletion{}
	syncer := &namespaceDeletionSyncer{config: Config{HostedClusterRemovalTimeout: time.Hour}}

	if err := syncer.waitForHostedClusterRemoval(request, observation); err != nil {
		t.Fatal(err)
	}
	condition := apimeta.FindStatusCondition(request.Status.Conditions, fleetapi.ConditionTypeNamespaceDeletionComplete)
	if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != reasonWaitingForHostedClusterRemoval {
		t.Fatalf("already-deleting HostedCluster must keep the namespace pending, got %+v", condition)
	}
	if request.Status.HostedClusterDeleteIssuedAt != nil {
		t.Fatal("must not claim this request issued a delete")
	}
}
