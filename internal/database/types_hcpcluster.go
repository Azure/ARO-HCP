package database

import (
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// HCPClusterDocument represents a customer desired HCP Cluster.
// To transition from our current state using cluster-service as half the source of truth to a state where
// cosmos contains all the desired state and all the observed state, we are basing the schema on ResourceDocument.
type HCPClusterDocument struct {
	ResourceDocument `json:",inline"`

	// CustomerDesiredState represents information from the user.  Some minimal validation may happen prior to persisting
	// this information, but it is entirely possible that this state does not reflect an achievable reality.
	// This information is necessary to determine the HostedCluster and supporting resources that are desired.
	CustomerDesiredState CustomerDesiredState `json:"customerDesiredState"`

	ServiceProviderState ServiceProviderState `json:"serviceProviderState"`
}

func NewHCPClusterDocument(resourceID *azcorearm.ResourceID) *HCPClusterDocument {
	return &HCPClusterDocument{
		ResourceDocument: ResourceDocument{
			ResourceID: resourceID,
		},
	}
}

// GetValidTypes returns the valid resource types for a ResourceDocument.
func (doc HCPClusterDocument) GetValidTypes() []string {
	return []string{
		api.ClusterResourceType.String(),
	}
}

func (doc HCPClusterDocument) GetResourceType() azcorearm.ResourceType {
	return api.ClusterResourceType

}

type CustomerDesiredState struct {
	// HCPOpenShiftCluster contains the desired state from a customer.  It is filtered to only those fields that customers
	// are able to set.
	// We will whitelist specific fields as we go through to prevent conflicts.
	HCPOpenShiftCluster api.HCPOpenShiftCluster `json:"hcpOpenShiftCluster"`
}

type ServiceProviderState struct {
	// HCPOpenShiftCluster contains the service provider owned state.  It is filtered to only those fields that the service provider owns.
	// We will whitelist specific fields as we go through to prevent conflicts.
	HCPOpenShiftCluster api.HCPOpenShiftCluster `json:"hcpOpenShiftCluster"`
}

// ClearUnownedFields force resets fields in the document down to only the allowed fields.  We do this because our
// internal API types are not generated and so it's significant work to create types for customer versus service provider.
func ClearUnownedFields(in *HCPClusterDocument) {
	in.CustomerDesiredState.HCPOpenShiftCluster = KeepCustomerOwnedFieldsFromHCPOpenShiftCluster(in.CustomerDesiredState.HCPOpenShiftCluster)
	in.ServiceProviderState.HCPOpenShiftCluster = KeepServiceProviderOwnedFieldsFromHCPOpenShiftCluster(in.ServiceProviderState.HCPOpenShiftCluster)
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
