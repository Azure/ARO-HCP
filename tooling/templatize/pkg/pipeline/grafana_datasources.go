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

package pipeline

import (
	"context"
	"fmt"

	"github.com/Azure/ARO-Tools/pipelines/graph"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/modify"
)

func runGrafanaDatasourcesStep(_ graph.Identifier, step *types.GrafanaDatasourcesStep, ctx context.Context, options *StepRunOptions, executionTarget ExecutionTarget, _ *ExecutionState) error {
	opts := modify.DefaultAddDatasourceOptions()
	opts.GrafanaName = step.GrafanaName
	opts.SubscriptionID = executionTarget.GetSubscriptionID()
	opts.ResourceGroup = executionTarget.GetResourceGroup()

	validated, err := opts.Validate(ctx)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return fmt.Errorf("completion failed: %w", err)
	}

	if err := completed.Run(ctx); err != nil {
		return fmt.Errorf("grafana datasources step failed: %w", err)
	}

	return nil
}
