package arm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

type Subscription struct {
	// The resource provider contract gives an example RegistrationDate
	// in RFC1123 format but does not explicitly state a required format
	// so we leave it a plain string.
	State            SubscriptionState       `json:"state"            validate:"required_for_put,enum_subscriptionstate"`
	RegistrationDate *string                 `json:"registrationDate" validate:"required_for_put"`
	Properties       *SubscriptionProperties `json:"properties"`

	// LastUpdated is a copy of the Cosmos DB system generated
	// "_ts" last updated timestamp field for metrics reporting.
	LastUpdated int `json:"-"`
}

// GetValidTypes returns the valid resource types for a Subscription.
func (s Subscription) GetValidTypes() []string {
	return []string{azcorearm.SubscriptionResourceType.String()}
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
	AdditionalProperties *map[string]string   `json:"additionalProperties,omitempty"`
}

type Feature struct {
	Name  *string `json:"name,omitempty"`
	State *string `json:"state,omitempty"`
}

type AvailabilityZone struct {
	Location     *string        `json:"location,omitempty"`
	ZoneMappings *[]ZoneMapping `json:"zoneMppings,omitempty"`
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
