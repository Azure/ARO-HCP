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

// ResourceGroup represents a group of steps, targeting one resource group in one subscription.
type ResourceGroup struct {
	*ResourceGroupMeta `json:",inline"`

	SubscriptionProvisioning *SubscriptionProvisioning `json:"subscriptionProvisioning,omitempty"`

	Steps           Steps           `json:"steps"`
	ValidationSteps ValidationSteps `json:"validationSteps,omitempty"`
}

// ResourceGroupMeta holds metadata required to fully qualify a resource group execution context. Subscription provisioning
// is explicitly omitted as any code in which execution context is important is necessarily a space in which it is not safe
// to run subscription provisioning, as only one subscription provisioning block is allowed per Ev2 rollout.
type ResourceGroupMeta struct {
	// Name is the semantic identifier for this group of steps - it must be short and should be shared between pipelines that
	// do work in the same Azure resource group.
	Name string `json:"name"`
	// ResourceGroup is the name of the Azure resource group in which work will occur.
	ResourceGroup string `json:"resourceGroup"`
	// Subscription is the subscription *key* in which work will occur.
	Subscription string `json:"subscription"`
	// Stamped declares that this resource group is stamp-scoped — it is deployed once per stamp.
	// Unstamped services must not define or depend on stamped resource groups.
	Stamped bool `json:"stamped,omitempty"`

	// ExecutionConstraints define a set of constraints on where this pipeline should be executed.
	// If unset, the default behavior is to deploy to all clouds, environments, regions, and stamps.
	// The set of constraints are evaluated using logical OR - adding to the list adds a set of possible
	// deployment environments.
	ExecutionConstraints []ExecutionConstraint `json:"executionConstraints,omitempty"`
}

// ExecutionConstraint defines a set of parameters for which the pipeline should execute. For each type of parameter,
// values are evaluated with logical OR - e.g. specifying two clouds will specify either cloud. The different types
// of parameters are evaluated with logical AND - e.g., specifying a cloud and an environment will constrain the
// pipeline to run in that cloud AND that environment.
type ExecutionConstraint struct {
	// Singleton defines this pipeline to run once - ever - in the entire history of the universe. No re-deployments
	// will be allowed.
	Singleton bool `json:"singleton"`

	// Level defines the ev2 execution constraint level ("Cloud" or "Region")
	// By default, if one or fewer regions is omitted, the level will be inferred as Cloud.
	// If a deployment does not specify regions, but it should go to every region in that cloud, the level should be set to "Region".
	Level string `json:"level,omitempty"`

	// Clouds define the clouds in which this pipeline should run. If unset, execution will be unconstrained across clouds.
	Clouds []string `json:"clouds,omitempty"`
	// Environments define the environments in which this pipeline should run, for the given clouds. If unset, execution will be unconstrained across environments.
	Environments []string `json:"environments,omitempty"`
	// Regions define the regions in which this pipeline should run, for the given clouds and environments. If unset, execution will be unconstrained across regions.
	Regions []string `json:"regions,omitempty"`
}

func (rg *ResourceGroup) Validate() error {
	if rg.ResourceGroupMeta == nil {
		return fmt.Errorf("resource group metadata is required")
	}
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
		case StepActionShell:
			step = &ShellStep{}
		case StepActionSafeFly:
			step = &SafeFlyStep{}
		case StepActionHelm:
			step = &HelmStep{}
		case StepActionARM:
			step = &ARMStep{}
		case StepActionARMStack:
			step = &ARMStackStep{}
		case StepActionDelegateChildZone:
			step = &DelegateChildZoneStep{}
		case StepActionSetCertificateIssuer:
			step = &SetCertificateIssuerStep{}
		case StepActionCreateCertificate:
			step = &CreateCertificateStep{}
		case StepActionResourceProviderRegistration:
			step = &ResourceProviderRegistrationStep{}
		case StepActionImageMirror:
			step = &ImageMirrorStep{}
		case StepActionRPLogs, StepActionClusterLogs:
			step = &LogsStep{}
		case StepActionFeatureRegistration:
			step = &FeatureRegistrationStep{}
		case StepActionProviderFeatureRegistration:
			step = &ProviderFeatureRegistrationStep{}
		case StepActionEv2Registration:
			step = &Ev2RegistrationStep{}
		case StepActionSecretSync:
			step = &SecretSyncStep{}
		case StepActionKusto:
			step = &KustoStep{}
		case StepActionPav2:
			step = &Pav2Step{}
		case StepActionPublishGenevaAction:
			step = &PublishGenevaActionStep{}
		case StepActionRunGenevaAction:
			step = &RunGenevaActionStep{}
		case StepActionGenevaHealth:
			step = &GenevaHealthStep{}
		case StepActionPublishGenevaAutomation:
			step = &PublishGenevaAutomationStep{}
		case StepActionProwJob:
			step = &ProwJobStep{}
		case StepActionGrafanaDashboards:
			step = &GrafanaDashboardsStep{}
		case StepActionGrafanaManage:
			step = &GrafanaManageStep{}
		case StepActionGrafanaDatasources:
			step = &GrafanaDatasourcesStep{}
		case StepActionKustoEntityGroups:
			step = &KustoEntityGroupsStep{}
		case StepActionIstioUpgrade:
			step = &IstioUpgradeStep{}
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

type ValidationSteps []ValidationStep

func (s *ValidationSteps) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("failed to unmarshal %v into array of json.RawMessage: %w", string(data), err)
	}

	steps := make([]ValidationStep, 0, len(raw))
	for i, rawStep := range raw {
		stepMeta := &StepMeta{}
		if err := yaml.Unmarshal(rawStep, stepMeta); err != nil {
			return fmt.Errorf("steps[%d]: failed to unmarshal step metadata from raw json: %w", i, err)
		}

		var step ValidationStep
		switch stepMeta.Action {
		case StepActionShell:
			step = &ShellValidationStep{}
		case StepActionProwJob:
			step = &ProwJobValidationStep{}
		default:
			step = &GenericValidationStep{}
		}
		if err := yaml.Unmarshal(rawStep, step); err != nil {
			return fmt.Errorf("steps[%d]: failed to unmarshal step from metadata remainder: %w", i, err)
		}
		steps = append(steps, step)
	}
	*s = steps
	return nil
}
