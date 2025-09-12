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
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// HCPCluster represents a customer desired HCP Cluster.
// To transition from our current state using cluster-service as half the source of truth to a state where
// cosmos contains all the desired state and all the observed state, we are basing the schema on ResourceDocument.
type HCPCluster struct {
	TypedDocument `json:",inline"`
	Properties    HCPClusterProperties `json:"properties"`
}

type HCPClusterProperties struct {
	ResourceDocument `json:",inline"`

	// CustomerDesiredState represents information from the user.  Some minimal validation may happen prior to persisting
	// this information, but it is entirely possible that this state does not reflect an achievable reality.
	// This information is necessary to determine the HostedCluster and supporting resources that are desired.
	CustomerDesiredState CustomerDesiredHCPClusterState `json:"customerDesiredState"`

	ServiceProviderState ServiceProviderHCPClusterState `json:"serviceProviderState"`
}

var _ DocumentProperties = &HCPCluster{}

func NewHCPClusterDocument(resourceID *azcorearm.ResourceID) *HCPCluster {
	return &HCPCluster{
		Properties: HCPClusterProperties{
			ResourceDocument: ResourceDocument{
				ResourceID: resourceID,
			},
		},
	}
}

func (doc *HCPCluster) GetTypedDocument() *TypedDocument {
	return &doc.TypedDocument
}

func (doc *HCPCluster) GetResourceDocument() *ResourceDocument {
	return &doc.Properties.ResourceDocument
}

func (doc *HCPCluster) GetReportingID() string {
	return doc.GetResourceDocument().ResourceID.String()
}

func (doc *HCPCluster) GetResourceType() azcorearm.ResourceType {
	return api.ClusterResourceType
}

func (doc *HCPCluster) GetSubscriptionID() string {
	return doc.TypedDocument.PartitionKey
}

func (doc *HCPCluster) GetResourceID() *azcorearm.ResourceID {
	return doc.Properties.ResourceDocument.ResourceID
}

func (doc *HCPCluster) SetTypedDocument(in TypedDocument) {
	doc.TypedDocument = in
}

func (doc *HCPCluster) SetResourceID(resourceID *azcorearm.ResourceID) {
	doc.Properties.ResourceDocument.ResourceID = resourceID
}

type CustomerDesiredHCPClusterState struct {
	// HCPOpenShiftCluster contains the desired state from a customer.  It is filtered to only those fields that customers
	// are able to set.
	// We will whitelist specific fields as we go through to prevent conflicts.
	HCPOpenShiftCluster api.HCPOpenShiftCluster `json:"hcpOpenShiftCluster"`
}

type ServiceProviderHCPClusterState struct {
	// HCPOpenShiftCluster contains the service provider owned state.  It is filtered to only those fields that the service provider owns.
	// We will whitelist specific fields as we go through to prevent conflicts.
	HCPOpenShiftCluster api.HCPOpenShiftCluster `json:"hcpOpenShiftCluster"`
}

// ClearUnownedFields force resets fields in the document down to only the allowed fields.  We do this because our
// internal API types are not generated and so it's significant work to create types for customer versus service provider.
func ClearUnownedFields(in *HCPCluster) {
	in.Properties.CustomerDesiredState.HCPOpenShiftCluster = KeepCustomerOwnedFieldsFromHCPOpenShiftCluster(in.Properties.CustomerDesiredState.HCPOpenShiftCluster)
	in.Properties.ServiceProviderState.HCPOpenShiftCluster = KeepServiceProviderOwnedFieldsFromHCPOpenShiftCluster(in.Properties.ServiceProviderState.HCPOpenShiftCluster)
}

// KeepCustomerOwnedFieldsFromHCPOpenShiftCluster creates a new instance and copies the data that a customer is allowed to set.
// This approach makes the whitelist extremely clear.
// Once the vast majority of fields are customer owned, we can choose to invert the model.
// TODO filter using the create/update (but not read) tagging on api.HCPOpenShiftCluster struct
func KeepCustomerOwnedFieldsFromHCPOpenShiftCluster(in api.HCPOpenShiftCluster) api.HCPOpenShiftCluster {
	out := api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Location: in.Location,
		},
		Properties: api.HCPOpenShiftClusterProperties{
			DNS: api.DNSProfile{
				BaseDomainPrefix: in.Properties.DNS.BaseDomain,
			},
		},
	}

	return out
}

// KeepServiceProviderOwnedFieldsFromHCPOpenShiftCluster creates a new instance and copies the data that a service provider is allowed to set.
// This approach makes the whitelist extremely clear.
// Once the vast majority of fields are customer owned, we can choose to invert the model.
// TODO filter using the read (but not create/update) tagging on api.HCPOpenShiftCluster struct
func KeepServiceProviderOwnedFieldsFromHCPOpenShiftCluster(in api.HCPOpenShiftCluster) api.HCPOpenShiftCluster {
	out := api.HCPOpenShiftCluster{
		Properties: api.HCPOpenShiftClusterProperties{
			DNS: api.DNSProfile{
				BaseDomain: in.Properties.DNS.BaseDomain,
			},
		},
	}

	return out
}
