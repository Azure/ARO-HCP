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

package api

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// DNSReservation is a logical (not real) resource that exists at a subscription level to provide a simple means of reserving a DNS reservation.
// It logically belongs
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type DNSReservation struct {
	// CosmosMetadata ResourceID is nested under the cluster so that association and cleanup work as expected
	// it will be the ServiceProviderCluster type and the name default
	CosmosMetadata `json:"cosmosMetadata"`

	// this matches the resourcedocument and standard storage schema.
	// we already store this field, but its currently done in conversion trickery.  Update to directly serialize it.
	// all items previously stored will read out and have this filled in.
	// we need to be sure that all new records have it too.
	ResourceID *azcorearm.ResourceID `json:"resourceId,omitempty"`

	// MustBindByTime is the time by which a ServiceProviderClusterStatus must have claimed this DNSReservation.
	// If a cleanup thread finds a DNSReservation that is not listed in a ServiceProviderClusterStatus after this time,
	// then the DNSReservation will be deleted.
	MustBindByTime *metav1.Time `json:"mustBindByTime"`

	// OwningCluster is the name of the cluster that this reservation is for.  This allows for easy cleanup after MustBindByTime
	// is expired.
	OwningCluster *azcorearm.ResourceID `json:"owningCluster,omitempty"`

	BindingState BindingState `json:"bindingState,omitempty"`

	// CleanupTime is the time after which this DNSReservation will be deleted.
	CleanupTime *metav1.Time `json:"cleanupTime,omitempty"`
}

type BindingState string

var (
	BindingStatePending         BindingState = "Pending"
	BindingStateBound           BindingState = "Bound"
	BindingStatePendingDeletion BindingState = "PendingDeletion"
)
