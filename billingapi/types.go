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

// Package billingapi defines the persisted contract shared by the ARO-HCP
// resource provider and the HCP Billing service. It has no storage-client or
// controller dependencies.
package billingapi

import (
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// BillingDocument identifies a cluster incarnation and its billing progress.
// Fields are persisted at the top level of the Cosmos document. Missing
// optional timestamps remain nil.
type BillingDocument struct {
	// Written by: ARO-HCP resource provider.
	ID string `json:"id,omitempty"`
	// SubscriptionID is also the billing container's partition key.
	// Written by: ARO-HCP resource provider.
	SubscriptionID string `json:"subscriptionId,omitempty"`
	// CreationTime is the initial billing boundary supplied by the RP.
	// Written by: ARO-HCP resource provider.
	CreationTime time.Time `json:"creationTime,omitempty"`
	// DeletionTime is the hosting billing cutoff supplied by the RP.
	// Written by: ARO-HCP resource provider.
	DeletionTime *time.Time `json:"deletionTime,omitempty"`
	// LastBillingTimeOfVMEvent is the compute event batch-processing watermark.
	// Written by: HCP Billing service after successful compute usage processing.
	LastBillingTimeOfVMEvent *time.Time `json:"lastBillingTimeOfVMEvent,omitempty"`
	// LastBillingTimeOfMngtEvent is the end of the last successfully billed hosting interval.
	// Written by: HCP Billing service after successful hosting usage processing.
	LastBillingTimeOfMngtEvent *time.Time `json:"lastBillingTimeOfMngtEvent,omitempty"`
	// Written by: ARO-HCP resource provider.
	Location string `json:"location,omitempty"`
	// Written by: ARO-HCP resource provider.
	TenantID string `json:"tenantId,omitempty"`
	// ResourceID is encoded as a JSON string by the Azure SDK. Construct it with
	// arm.ParseResourceID and treat the resulting resource ID as immutable.
	// Written by: ARO-HCP resource provider.
	ResourceID *azcorearm.ResourceID `json:"resourceId,omitempty"`
	// ManagedResourceGroup is the ARM resource ID of the cluster's managed RG.
	// Written by: ARO-HCP resource provider.
	ManagedResourceGroup string `json:"managedResourceGroup,omitempty"`
}

// DeepCopy returns an independent copy of the document.
func (in *BillingDocument) DeepCopy() *BillingDocument {
	if in == nil {
		return nil
	}
	out := *in
	if in.DeletionTime != nil {
		value := *in.DeletionTime
		out.DeletionTime = &value
	}
	if in.LastBillingTimeOfVMEvent != nil {
		value := *in.LastBillingTimeOfVMEvent
		out.LastBillingTimeOfVMEvent = &value
	}
	if in.LastBillingTimeOfMngtEvent != nil {
		value := *in.LastBillingTimeOfMngtEvent
		out.LastBillingTimeOfMngtEvent = &value
	}
	out.ResourceID = copyResourceID(in.ResourceID)
	return &out
}

func copyResourceID(in *azcorearm.ResourceID) *azcorearm.ResourceID {
	if in == nil {
		return nil
	}
	out := *in
	out.Parent = copyResourceID(in.Parent)
	return &out
}
