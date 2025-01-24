package arm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"encoding/json"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// Operation is an ARM-defined resource returned by operation status endpoints.
type Operation struct {
	ID              *azcorearm.ResourceID `json:"id,omitempty"`
	Name            string                `json:"name,omitempty"`
	Status          ProvisioningState     `json:"status"`
	StartTime       *time.Time            `json:"startTime,omitempty"`
	EndTime         *time.Time            `json:"endTime,omitempty"`
	PercentComplete float64               `json:"percentComplete,omitempty"`
	Properties      json.RawMessage       `json:"peroperties,omitempty"`
	Error           *CloudErrorBody       `json:"error,omitempty"`
	Operations      []Operation           `json:"operations,omitempty"`
}
