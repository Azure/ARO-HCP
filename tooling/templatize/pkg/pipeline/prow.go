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
	"strconv"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-Tools/config/ev2config"
	"github.com/Azure/ARO-Tools/pipelines/types"
	prowjobexecutor "github.com/Azure/ARO-Tools/tools/prow-job-executor"
)

func runProwJobStep(step *types.ProwJobStep, ctx context.Context, options *StepRunOptions, executionTarget ExecutionTarget) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return err
	}
	if options.DryRun {
		logger.Info("Skipping Prow Job step for dry-run.")
		return nil
	}

	ev2cfg, err := ev2config.ResolveConfig(options.Cloud, executionTarget.GetRegion())
	if err != nil {
		return fmt.Errorf("cannot resolve config for %s/%s: %v", err, options.Cloud, executionTarget.GetRegion())
	}
	rawKeyVaultDNSSuffix, err := ev2cfg.GetByPath("keyVault.domainNameSuffix")
	if err != nil {
		return fmt.Errorf("cannot get Ev2 config value keyVault.domainNameSuffix: %v", err)
	}
	keyVaultDNSSuffix, ok := rawKeyVaultDNSSuffix.(string)
	if !ok {
		return fmt.Errorf("keyVaultDNSSuffix is %T, not a string", rawKeyVaultDNSSuffix)
	}
	keyVaultURI := "https://" + step.TokenKeyvault + "." + keyVaultDNSSuffix

	gate, err := strconv.ParseBool(step.GatePromotion)
	if err != nil {
		return fmt.Errorf("could not parse gate promotion flag: %w", err)
	}

	opts := prowjobexecutor.DefaultExecuteOptions()
	opts.Secret = step.TokenSecret
	opts.KeyVaultURI = keyVaultURI
	opts.ProwJobName = step.JobName
	opts.GatePromotion = gate

	inputs := prowInputs{
		KeyVault: keyVaultURI,
		Secret:   step.TokenSecret,
		JobName:  step.JobName,
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
		return err
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}

	logger.Info("Starting Prow job execution", "jobName", completed.ProwJobName, "region", completed.Region)

	if err := completed.Execute(ctx); err != nil {
		return err
	}

	return commit()
}

type prowInputs struct {
	KeyVault string `json:"keyVault"`
	Secret   string `json:"secret"`
	JobName  string `json:"jobName"`
}
