// Copyright 2026 Microsoft Corporation
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

package app

import (
	"context"
	"fmt"
	"os"

	k8soperation "k8s.io/apimachinery/pkg/api/operation"

	"sigs.k8s.io/yaml"

	apisconfigv1 "github.com/Azure/ARO-HCP/backend/pkg/apis/config/v1"
	"github.com/Azure/ARO-HCP/backend/pkg/operatorsmis"
)

func loadOperatorsManagedIdentitiesConfig(ctx context.Context, path string) (*apisconfigv1.OperatorsManagedIdentitiesConfig, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("configuration path is required")
	}

	rawBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("error reading file %s: %w", path, err)
	}

	var config apisconfigv1.OperatorsManagedIdentitiesConfig
	err = yaml.Unmarshal(rawBytes, &config)
	if err != nil {
		return nil, fmt.Errorf("error unmarshaling file %s: %w", path, err)
	}

	validationErrors := config.Validate(ctx, k8soperation.Operation{Type: k8soperation.Create})
	if len(validationErrors) > 0 {
		return nil,
			fmt.Errorf("error validating file %s: %w", path, validationErrors.ToAggregate())
	}

	return &config, nil
}

func NewOperatorsManagedIdentitiesConfig(ctx context.Context, operatorsManagedIdentitiesConfigPath string) (*operatorsmis.Config, error) {
	if len(operatorsManagedIdentitiesConfigPath) == 0 {
		return nil, nil
	}

	operatorsManagedIdentitiesConfig, err := loadOperatorsManagedIdentitiesConfig(ctx, operatorsManagedIdentitiesConfigPath)
	if err != nil {
		return nil, fmt.Errorf("error loading operators managed identities config: %w", err)
	}

	cfg := buildOperatorsManagedIdentitiesConfig(operatorsManagedIdentitiesConfig)
	return cfg, nil
}

// BuildOperatorsManagedIdentitiesConfig builds a new operatorsmis.Config from the given
// operatorsManagedIdentitiesConfig of type apisconfigv1.OperatorsManagedIdentitiesConfig.
func buildOperatorsManagedIdentitiesConfig(operatorsManagedIdentitiesConfig *apisconfigv1.OperatorsManagedIdentitiesConfig) *operatorsmis.Config {
	// Build the control plane operator identities.
	cpIDs := operatorsmis.NewControlPlaneOperatorsIdentities()
	for operatorName, controlPlaneOperatorIdentity := range operatorsManagedIdentitiesConfig.ControlPlaneOperatorsIdentities {
		roleDefinitions := []operatorsmis.RoleDefinition{}
		for _, roleDefinition := range controlPlaneOperatorIdentity.RoleDefinitions {
			roleDefinitions = append(roleDefinitions, *operatorsmis.NewRoleDefinition(roleDefinition.Name, roleDefinition.ResourceID))
		}

		cpIDs.Add(operatorsmis.NewControlPlaneOperatorIdentity(
			operatorName,
			controlPlaneOperatorIdentity.MinOpenShiftVersion,
			controlPlaneOperatorIdentity.MaxOpenShiftVersion,
			roleDefinitions,
			string(controlPlaneOperatorIdentity.Requirement),
		))
	}

	// Build the data plane operator identities.
	dpIDs := operatorsmis.NewDataPlaneOperatorsIdentities()
	for operatorName, dataPlaneOperatorIdentity := range operatorsManagedIdentitiesConfig.DataPlaneOperatorsIdentities {
		roleDefinitions := []operatorsmis.RoleDefinition{}
		for _, roleDefinition := range dataPlaneOperatorIdentity.RoleDefinitions {
			roleDefinitions = append(roleDefinitions, *operatorsmis.NewRoleDefinition(roleDefinition.Name, roleDefinition.ResourceID))
		}

		kubernetesServiceAccounts := []operatorsmis.KubernetesServiceAccount{}
		for _, kubernetesServiceAccount := range dataPlaneOperatorIdentity.KubernetesServiceAccounts {
			kubernetesServiceAccounts = append(kubernetesServiceAccounts, operatorsmis.NewKubernetesServiceAccount(kubernetesServiceAccount.Name, kubernetesServiceAccount.Namespace))
		}

		dpIDs.Add(operatorsmis.NewDataPlaneOperatorIdentity(
			operatorName,
			dataPlaneOperatorIdentity.MinOpenShiftVersion,
			dataPlaneOperatorIdentity.MaxOpenShiftVersion,
			roleDefinitions,
			string(dataPlaneOperatorIdentity.Requirement),
			kubernetesServiceAccounts,
		))
	}

	// Build and return the operatorsmis.Config.
	cfg := operatorsmis.NewOperatorsManagedIdentitiesConfig(cpIDs, dpIDs)
	return &cfg
}
