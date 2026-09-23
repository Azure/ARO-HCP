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

package billingcosmosstorage

import (
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

// BillingDocument records timestamps of Hosted Control Plane OpenShift cluster
// creation and deletion for the purpose of customer billing.
type BillingDocument struct {
	cosmosstorageutils.BaseDocument

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
		BaseDocument: cosmosstorageutils.BaseDocument{
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

// BillingDocumentList is a list of billing documents for use with Kubernetes informers.
type BillingDocumentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	ResourceVersion string
	Items           []BillingDocument `json:"items"`
}

// GetObjectKind returns the object kind for BillingDocument.
func (in *BillingDocument) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

// GetObjectMeta returns metadata that allows Kubernetes informer machinery
// to key billing documents by their ID.
func (in *BillingDocument) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	om.Name = strings.ToLower(in.ID)
	return om
}

// GetObjectKind returns the object kind for BillingDocumentList.
func (l *BillingDocumentList) GetObjectKind() schema.ObjectKind {
	return &l.TypeMeta
}

// DeepCopy creates a deep copy of the BillingDocument.
func (in *BillingDocument) DeepCopy() *BillingDocument {
	if in == nil {
		return nil
	}
	out := new(BillingDocument)
	*out = *in

	if in.DeletionTime != nil {
		out.DeletionTime = new(time.Time)
		*out.DeletionTime = *in.DeletionTime
	}

	if in.ResourceID != nil {
		out.ResourceID = new(azcorearm.ResourceID)
		*out.ResourceID = *in.ResourceID
	}

	return out
}

// DeepCopyObject creates a deep copy of the BillingDocument as runtime.Object.
func (in *BillingDocument) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopy creates a deep copy of the BillingDocumentList.
func (in *BillingDocumentList) DeepCopy() *BillingDocumentList {
	if in == nil {
		return nil
	}
	out := new(BillingDocumentList)
	*out = *in

	if in.Items != nil {
		out.Items = make([]BillingDocument, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopy()
			out.Items[i] = *in.Items[i].DeepCopy()
		}
	}

	return out
}

// DeepCopyObject creates a deep copy of the BillingDocumentList as runtime.Object.
func (in *BillingDocumentList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}
