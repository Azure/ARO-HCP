// Copyright 2025 Microsoft Corporation
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

package coreapi

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// DNSReservation is a logical (not real Azure) resource that exists directly
// under a subscription to reserve a unique kube-apiserver DNS name for a cluster.
//
// The resource ID takes the form:
//
//	/subscriptions/<sub>/providers/Microsoft.RedHatOpenShift/dnsReservations/<name>
//
// It deliberately lives under the subscription rather than under the cluster so
// that a reservation can outlive the cluster that owned it. After a cluster is
// deleted we keep its reservation for a one-week cooldown (BindingState
// PendingDeletion with CleanupTime set) before the name returns to the pool.
// This prevents a freshly-created cluster from immediately re-acquiring a DNS
// name that resolvers and customers may still associate with the old cluster.
//
// The Cosmos document `id` is derived deterministically from the ResourceID, so
// the subscription-partitioned container enforces uniqueness of the reservation
// name within a subscription: a second Create of the same
// `<baseDomainPrefix>.<random>` name fails with HTTP 409 Conflict. That is the
// primitive the DNSReservationController relies on to guarantee two clusters can
// never bind the same name.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type DNSReservation struct {
	// CosmosMetadata carries the ResourceID (the DNSReservation's own resource
	// ID), the CosmosETag, and the auto-incrementing InstanceVersion used by the
	// storage layer. PartitionKey holds the lowercased subscriptionID.
	CosmosMetadata `json:"cosmosMetadata"`

	// MustBindByTime is the deadline by which a ServiceProviderClusterStatus must
	// have claimed this DNSReservation (i.e. the reservation must have reached
	// BindingStateBound). The creation controller sets it to now+61m. If the
	// cleanup controller finds a still-Pending reservation whose owning cluster
	// never pointed at it and this deadline has passed, it deletes the
	// reservation so the name returns to the pool. It is cleared once the
	// reservation is bound.
	// Written by: DNSReservationController, DNSReservationCleanupController
	MustBindByTime *metav1.Time `json:"mustBindByTime,omitempty"`

	// OwningCluster is the resource ID of the HCPOpenShiftCluster this reservation
	// was created for. It lets the cleanup controller locate the owning
	// ServiceProviderCluster and decide whether the reservation is still needed.
	// Written by: DNSReservationController
	OwningCluster *azcorearm.ResourceID `json:"owningCluster,omitempty"`

	// BindingState tracks the reservation lifecycle:
	// Pending -> Bound -> PendingDeletion -> (deleted).
	// Written by: DNSReservationController, DNSReservationCleanupController
	BindingState BindingState `json:"bindingState,omitempty"`

	// CleanupTime, when non-nil and in the past, tells the cleanup controller to
	// delete this reservation. It is set to now+1week when a bound reservation's
	// owning cluster is gone (or has moved to a different reservation),
	// implementing the one-week DNS-name reuse cooldown.
	// Written by: DNSReservationCleanupController
	CleanupTime *metav1.Time `json:"cleanupTime,omitempty"`
}

// BindingState enumerates the lifecycle states of a DNSReservation.
type BindingState string

const (
	// BindingStatePending means the reservation's name has been uniquely claimed
	// in Cosmos, but no ServiceProviderCluster yet points at it. A reservation
	// that is never claimed by MustBindByTime is reaped by the cleanup controller.
	BindingStatePending BindingState = "Pending"

	// BindingStateBound means a ServiceProviderCluster.Status.KubeAPIServerDNSReservation
	// points at this reservation; the DNS name is in active use by a live cluster.
	BindingStateBound BindingState = "Bound"

	// BindingStatePendingDeletion means the reservation is scheduled for deletion
	// at CleanupTime; the DNS name is in its one-week reuse cooldown after the
	// owning cluster went away or moved to a different reservation.
	BindingStatePendingDeletion BindingState = "PendingDeletion"
)
