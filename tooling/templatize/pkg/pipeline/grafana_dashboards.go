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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-Tools/pipelines/graph"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/sync"
	"github.com/Azure/ARO-Tools/tools/grafanactl/config"
)

func runGrafanaDashboardsStep(id graph.Identifier, step *types.GrafanaDashboardsStep, ctx context.Context, options *StepRunOptions, executionTarget ExecutionTarget, state *ExecutionState) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return err
	}

	state.RLock()
	outputs := state.Outputs
	state.RUnlock()

	value, err := resolveInput(id.ServiceGroup, step.GrafanaName, outputs)
	if err != nil {
		return fmt.Errorf("could not resolve grafana name: %w", err)
	}
	grafanaName, ok := value.(string)
	if !ok {
		return fmt.Errorf("grafana name is %T, not a string", value)
	}

	observabilityPath := filepath.Join(options.PipelineDirectory, step.ObservabilityConfig)

	opts := sync.DefaultSyncDashboardsOptions()
	opts.GrafanaName = grafanaName
	opts.SubscriptionID = executionTarget.GetSubscriptionID()
	opts.ResourceGroup = executionTarget.GetResourceGroup()
	opts.DryRun = options.DryRun
	opts.ConfigFilePath = observabilityPath

	cfg, err := config.LoadFromFile(observabilityPath)
	if err != nil {
		return err
	}

	hash := sha256.New()
	for _, dashboard := range cfg.GrafanaDashboards.DashboardFolders {
		dir := filepath.Join(filepath.Dir(observabilityPath), dashboard.Path)
		if err := fs.WalkDir(os.DirFS(dir), ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			raw, err := os.ReadFile(filepath.Join(dir, path))
			if err != nil {
				return fmt.Errorf("could not read file %s: %w", path, err)
			}
			hash.Write(raw)
			return nil
		}); err != nil {
			return fmt.Errorf("could not walk dashboard folder: %s: %w", dir, err)
		}
	}
	hashBytes := hash.Sum(nil)
	digest := hex.EncodeToString(hashBytes)

	inputs := grafanaDashboardsInputs{
		GrafanaName:      opts.GrafanaName,
		SubscriptionID:   opts.SubscriptionID,
		ResourceGroup:    opts.ResourceGroup,
		DashboardsDigest: digest,
	}
	skip, commit, err := checkSentinel(logger, inputs, options.StepCacheDir)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	validated, err := opts.Validate(ctx)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return fmt.Errorf("completion failed: %w", err)
	}

	if err := completed.Run(ctx); err != nil {
		return fmt.Errorf("grafana dashboards step failed: %w", err)
	}

	return commit()
}

type grafanaDashboardsInputs struct {
	GrafanaName      string
	SubscriptionID   string
	ResourceGroup    string
	DashboardsDigest string
}
