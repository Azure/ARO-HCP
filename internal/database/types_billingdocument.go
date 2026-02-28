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

package database

import (
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

// BillingDocument records timestamps of Hosted Control Plane OpenShift cluster
// creation and deletion for the purpose of customer billing.
type BillingDocument struct {
	BaseDocument

	// The cluster creation time represents the time when the cluster was provisioned successfully
	CreationTime time.Time `json:"creationTime,omitempty"`
	// The cluster deletion time
	DeletionTime *time.Time `json:"deletionTime,omitempty"`

	// The location of the HCP cluster
	Location string `json:"location,omitempty"`
	// The tenant ID of the HCP cluster
	TenantID string `json:"tenantId,omitempty"`
	// The subscription ID of the HCP cluster (also the partition key)
	SubscriptionID string `json:"subscriptionId,omitempty"`
	// The HCP cluster ARM resource ID
	ResourceID *azcorearm.ResourceID `json:"resourceId,omitempty"`
	// The ARM resource ID of the managed resource group of the HCP cluster
	ManagedResourceGroup string `json:"managedResourceGroup,omitempty"`
}

func NewBillingDocument(id string, resourceID *azcorearm.ResourceID) *BillingDocument {
	return &BillingDocument{
		BaseDocument:  BaseDocument{
			ID: id,
		},
		SubscriptionID: resourceID.SubscriptionID,
		ResourceID:     resourceID,
	}
}

// BillingDocumentPatchOperations represents a patch request for a BillingDocument.
type BillingDocumentPatchOperations struct {
	azcosmos.PatchOperations
}

// SetDeletionTime appends a set operation for the DeletionTime field.
func (p *BillingDocumentPatchOperations) SetDeletionTime(deletionTime time.Time) {
	p.AppendSet("/deletionTime", deletionTime)
}
