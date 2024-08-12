package arm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

type Subscription struct {
	State            SubscriptionState       `json:"state"`
	RegistrationDate *string                 `json:"registrationDate,omitempty"`
	Properties       *SubscriptionProperties `json:"properties,omitempty"`
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
