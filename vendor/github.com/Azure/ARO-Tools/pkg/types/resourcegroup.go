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

package types

import (
	"encoding/json"
	"fmt"

	"sigs.k8s.io/yaml"
)

// ResourceGroup represents the resourcegroup containing all steps
type ResourceGroup struct {
	Name                     string                    `json:"name"`
	Subscription             string                    `json:"subscription"`
	SubscriptionProvisioning *SubscriptionProvisioning `json:"subscriptionProvisioning,omitempty"`
	// Deprecated: AKSCluster to be removed
	AKSCluster string `json:"aksCluster,omitempty"`
	Steps      Steps  `json:"steps"`
}

func (rg *ResourceGroup) Validate() error {
	if rg.Name == "" {
		return fmt.Errorf("resource group name is required")
	}
	if rg.Subscription == "" {
		return fmt.Errorf("subscription is required")
	}
	return nil
}

type Steps []Step

func (s *Steps) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("failed to unmarshal %v into array of json.RawMessage: %w", string(data), err)
	}

	steps := make([]Step, 0, len(raw))
	for i, rawStep := range raw {
		stepMeta := &StepMeta{}
		if err := yaml.Unmarshal(rawStep, stepMeta); err != nil {
			return fmt.Errorf("steps[%d]: failed to unmarshal step metadata from raw json: %w", i, err)
		}

		var step Step
		switch stepMeta.Action {
		case "Shell":
			step = &ShellStep{}
		case "ARM":
			step = &ARMStep{}
		case "DelegateChildZone":
			step = &DelegateChildZoneStep{}
		case "SetCertificateIssuer":
			step = &SetCertificateIssuerStep{}
		case "CreateCertificate":
			step = &CreateCertificateStep{}
		case "ResourceProviderRegistration":
			step = &ResourceProviderRegistrationStep{}
		case "ImageMirror":
			step = &ImageMirrorStep{}
		case "RPLogsAccount", "ClusterLogsAccount":
			step = &LogsStep{}
		case "FeatureRegistration":
			step = &FeatureRegistrationStep{}
		case "ProviderFeatureRegistration":
			step = &ProviderFeatureRegistrationStep{}
		case "Ev2Registration":
			step = &Ev2RegistrationStep{}
		case "SecretSync":
			step = &SecretSyncStep{}
		default:
			step = &GenericStep{}
		}
		if err := yaml.Unmarshal(rawStep, step); err != nil {
			return fmt.Errorf("steps[%d]: failed to unmarshal step from metadata remainder: %w", i, err)
		}
		steps = append(steps, step)
	}
	*s = steps
	return nil
}
