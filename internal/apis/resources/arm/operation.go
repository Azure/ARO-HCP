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
	Properties      json.RawMessage       `json:"properties,omitempty"`
	Error           *CloudErrorBody       `json:"error,omitempty"`
	Operations      []Operation           `json:"operations,omitempty"`
}
