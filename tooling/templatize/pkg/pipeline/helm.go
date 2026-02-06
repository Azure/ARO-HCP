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

package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/graph"
	"github.com/Azure/ARO-Tools/pkg/helm"
	"github.com/Azure/ARO-Tools/pkg/types"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/aks"
)

func runHelmStep(id graph.Identifier, step *types.HelmStep, ctx context.Context, options *StepRunOptions, executionTarget ExecutionTarget, state *ExecutionState) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return err
	}

	kubeconfig, err := aks.GetKubeConfig(ctx, executionTarget.GetSubscriptionID(), executionTarget.GetResourceGroup(), step.AKSCluster)
	if err != nil {
		return err
	}

	tmpdir, err := os.MkdirTemp(os.TempDir(), "helm-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpdir); err != nil {
			logger.Error(err, "Failed to clean up temporary directory.")
		}
	}()

	state.RLock()
	outputs := state.Outputs
	state.RUnlock()

	replacements := map[string]string{}
	for key, from := range step.InputVariables {
		value, err := resolveInput(id.ServiceGroup, from, outputs)
		if err != nil {
			return err
		}
		str, ok := value.(string)
		if !ok {
			return fmt.Errorf("input variable %s is %T, not a string", key, value)
		}
		replacements[key] = str
	}

	var kustoEndpointString string
	if step.KustoEndpoint != nil {
		kustoEndpointResolved, err := resolveInput(id.ServiceGroup, *(step.KustoEndpoint), outputs)
		if err != nil {
			return err
		}
		var ok bool
		kustoEndpointString, ok = kustoEndpointResolved.(string)
		if !ok {
			return fmt.Errorf("kusto endpoint variable is not a string, value: %T", kustoEndpointResolved)
		}
	}

	process := func(filepath string) ([]byte, error) {
		processed, err := config.PreprocessFile(filepath, options.Configuration)
		if err != nil {
			return nil, err
		}
		for from, to := range replacements {
			processed = bytes.ReplaceAll(processed, []byte(fmt.Sprintf("__%s__", from)), []byte(to))
		}
		return processed, nil
	}

	// first, pre-process the files provided by the user
	namespaceDir := filepath.Join(tmpdir, "namespaces")
	if err := os.MkdirAll(namespaceDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp dir for namespace manifests: %w", err)
	}
	var namespaceFiles []string
	namespaceContent := map[string][]byte{}
	for _, file := range step.NamespaceFiles {
		tmpfilename := strings.ReplaceAll(file, string(filepath.Separator), "-")
		tmpfile := filepath.Join(namespaceDir, tmpfilename)
		processed, err := process(filepath.Join(options.PipelineDirectory, file))
		if err != nil {
			return fmt.Errorf("failed to preprocess namespace manifest %s: %w", file, err)
		}
		if err := os.WriteFile(tmpfile, processed, 0644); err != nil {
			return fmt.Errorf("failed to write file %s: %w", file, err)
		}
		namespaceFiles = append(namespaceFiles, tmpfile)
		namespaceContent[tmpfilename] = processed
	}

	values := filepath.Join(tmpdir, filepath.Base(step.ValuesFile))
	processed, err := process(filepath.Join(options.PipelineDirectory, step.ValuesFile))
	if err != nil {
		return fmt.Errorf("failed to preprocess Helm values %s: %w", step.ValuesFile, err)
	}
	if err := os.WriteFile(values, processed, 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", values, err)
    }

	// Changes to support OCI Helm Images
	var chartDir string

	// If it is an OCI URL, we must pull it manually first.
    if strings.HasPrefix(step.ChartDir, "oci://") {
        cleanURL := strings.TrimSpace(step.ChartDir)

        args := []string{"pull", cleanURL}
        args = append(args, "--destination", tmpdir, "--untar")

        cmd := exec.CommandContext(ctx, "helm", args...)
        cmd.Env = os.Environ()

		logger.Info("Pulling OCI chart", "command", cmd.String())
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to pull OCI chart: %s: %w", string(out), err)
		}

		// Find the directory helm created
		entries, err := os.ReadDir(tmpdir)
		if err != nil {
			return fmt.Errorf("failed to read temp dir: %w", err)
		}

		found := false
		for _, e := range entries {
			if e.IsDir() && e.Name() != "namespaces" {
				chartDir = filepath.Join(tmpdir, e.Name())
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("helm pull succeeded but could not find extracted chart directory in %s", tmpdir)
		}

	} else {
		// Standard Local File Logic
		chartDir = filepath.Join(options.PipelineDirectory, step.ChartDir)
	}

	// Now chartDir is always a local folder (either original or pulled)
	chartData := map[string][]byte{}
	if err := filepath.WalkDir(chartDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		relpath, err := filepath.Rel(chartDir, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		chartData[relpath] = raw
		return nil
	}); err != nil {
		return fmt.Errorf("failed to walk helm chart dir: %w", err)
	}

	opts := helm.RawOptions{
		NamespaceFiles:    namespaceFiles,
		ReleaseName:       step.ReleaseName,
		ReleaseNamespace:  step.ReleaseNamespace,
		ChartDir:          chartDir,
		ValuesFile:        values,
		KustoDatabase:     step.KustoDatabase,
		KustoTable:        step.KustoTable,
		KustoEndpoint:     kustoEndpointString,
		Timeout:           5 * time.Minute,
		KubeconfigFile:    kubeconfig,
		DryRun:            options.DryRun,
		RollbackOnFailure: step.RollbackOnFailure,
	}

	inputs := helmInputs{
		SubscriptionId:   executionTarget.GetSubscriptionID(),
		ResourceGroup:    executionTarget.GetResourceGroup(),
		AKSClusterName:   step.AKSCluster,
		Namespaces:       namespaceContent,
		Chart:            chartData,
		Values:           processed,
		ReleaseName:      step.ReleaseName,
		ReleaseNamespace: step.ReleaseNamespace,
		DryRun:           options.DryRun,
	}
	skip, commit, err := checkSentinel(logger, inputs, options.StepCacheDir)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	validated, err := opts.Validate()
	if err != nil {
		return fmt.Errorf("failed to validate helm options: %w", err)
	}
	completed, err := validated.Complete()
	if err != nil {
		return fmt.Errorf("failed to complete helm options: %w", err)
	}
	if err := completed.Deploy(ctx); err != nil {
		return fmt.Errorf("failed to deploy helm release: %w", err)
	}

	return commit()
}

type helmInputs struct {
	SubscriptionId string `json:"subscriptionId"`
	ResourceGroup  string `json:"resourceGroup"`
	AKSClusterName string `json:"aksClusterName"`

	Namespaces       map[string][]byte `json:"namespaces"`
	Chart            map[string][]byte `json:"chart"`
	Values           []byte            `json:"values"`
	ReleaseName      string            `json:"releaseName"`
	ReleaseNamespace string            `json:"releaseNamespace"`
	DryRun           bool              `json:"dryRun"`
}
