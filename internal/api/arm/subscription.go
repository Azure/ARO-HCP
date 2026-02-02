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

package arm

import (
	"iter"
	"path"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// SubscriptionAPIVersion is the system API version for the subscription endpoint.
const SubscriptionAPIVersion = "2.0"

func ToSubscriptionResourceID(subscriptionName string) (*azcorearm.ResourceID, error) {
	return azcorearm.ParseResourceID(ToSubscriptionResourceIDString(subscriptionName))
}

func ToSubscriptionResourceIDString(subscriptionName string) string {
	return strings.ToLower(path.Join("/subscriptions", subscriptionName))
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type Subscription struct {
	CosmosMetadata `json:"cosmosMetadata"`

	ResourceID *azcorearm.ResourceID `json:"resourceId,omitempty"`

	// The resource provider contract gives an example RegistrationDate
	// in RFC1123 format but does not explicitly state a required format
	// so we leave it a plain string.
	State            SubscriptionState       `json:"state"`
	RegistrationDate *string                 `json:"registrationDate"`
	Properties       *SubscriptionProperties `json:"properties"`

	// LastUpdated is a copy of the Cosmos DB system generated
	// "_ts" last updated timestamp field for metrics reporting.
	LastUpdated int `json:"-"`
}

func (o *Subscription) GetCosmosData() *CosmosMetadata {
	return &CosmosMetadata{
		ResourceID: o.ResourceID,
	}
}

type SubscriptionProperties struct {
	TenantId             *string              `json:"tenantId,omitempty"`
	LocationPlacementId  *string              `json:"locationPlacementId,omitempty"`
	QuotaId              *string              `json:"quotaId,omitempty"`
	RegisteredFeatures   *[]Feature           `json:"registeredFeatures,omitempty"`
	AvailabilityZones    *AvailabilityZone    `json:"availabilityZones,omitempty"`
	SpendingLimit        *string              `json:"spendingLimit,omitempty"`
	AccountOwner         *AccountOwner        `json:"accountOwner,omitempty"`
	ManagedByTenants     *[]map[string]string `json:"managedByTenants,omitempty"`
	AdditionalProperties any                  `json:"additionalProperties,omitempty"`
}

type Feature struct {
	Name  *string `json:"name,omitempty"`
	State *string `json:"state,omitempty"`
}

type AvailabilityZone struct {
	Location     *string        `json:"location,omitempty"`
	ZoneMappings *[]ZoneMapping `json:"zoneMappings,omitempty"`
}

type ZoneMapping struct {
	LogicalZone  *string `json:"logicalZone,omitempty"`
	PhysicalZone *string `json:"physicalZone,omitempty"`
}

type AccountOwner struct {
	Puid  *string `json:"puid,omitempty"`
	Email *string `json:"-,omitempty"` // we don't need to nor want to serialize this field
}

type SubscriptionState string

const (
	SubscriptionStateRegistered   SubscriptionState = "Registered"
	SubscriptionStateUnregistered SubscriptionState = "Unregistered"
	SubscriptionStateWarned       SubscriptionState = "Warned"
	SubscriptionStateDeleted      SubscriptionState = "Deleted"
	SubscriptionStateSuspended    SubscriptionState = "Suspended"
)

var (
	ValidSubscriptionStates = sets.New(
		SubscriptionStateRegistered,
		SubscriptionStateUnregistered,
		SubscriptionStateWarned,
		SubscriptionStateDeleted,
		SubscriptionStateSuspended,
	)
)

// ListSubscriptionStates returns an iterator that yields all recognized
// SubscriptionState values. This function is intended as a test aid.
func ListSubscriptionStates() iter.Seq[SubscriptionState] {
	return slices.Values([]SubscriptionState{
		SubscriptionStateRegistered,
		SubscriptionStateUnregistered,
		SubscriptionStateWarned,
		SubscriptionStateDeleted,
		SubscriptionStateSuspended,
	})
}

// HasRegisteredFeature checks if a subscription has a specific feature registered.
// The feature name should be in the format "Microsoft.Provider/FeatureName".
// Returns true if the feature is present and its state is "Registered", false otherwise.
func (s *Subscription) HasRegisteredFeature(featureName string) bool {
	if s.Properties == nil || s.Properties.RegisteredFeatures == nil {
		return false
	}

	for _, feature := range *s.Properties.RegisteredFeatures {
		if feature.Name != nil && *feature.Name == featureName {
			if feature.State != nil && *feature.State == "Registered" {
				return true
			}
		}
	}

	return false
}
