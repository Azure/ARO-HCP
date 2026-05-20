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

package framework

import (
	"context"
	"fmt"
	"os"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/kube"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/utils/ptr"
)

// HelmChartConfig configures a Helm chart installation.
type HelmChartConfig struct {
	ReleaseName string
	RepoURL     string
	ChartName   string
	Version     string
	Namespace   string
	Values      map[string]any
}

// InstallHelmChart installs a Helm chart using the Helm Go SDK.
func InstallHelmChart(ctx context.Context, cfg HelmChartConfig, kubeconfigContent string) error {
	kubeconfigFile, err := os.CreateTemp("", "kubeconfig-helm-*.yaml")
	if err != nil {
		return fmt.Errorf("failed to create temp kubeconfig file: %w", err)
	}
	defer os.Remove(kubeconfigFile.Name())

	_, err = kubeconfigFile.WriteString(kubeconfigContent)
	if err != nil {
		return fmt.Errorf("failed to write kubeconfig content: %w", err)
	}

	if err := kubeconfigFile.Close(); err != nil {
		return fmt.Errorf("failed to close kubeconfig file: %w", err)
	}

	actionCfg := &action.Configuration{}
	cliOpts := &genericclioptions.ConfigFlags{
		KubeConfig: ptr.To(kubeconfigFile.Name()),
		Namespace:  ptr.To(cfg.Namespace),
	}
	if err := actionCfg.Init(cliOpts, cfg.Namespace, ""); err != nil {
		return fmt.Errorf("failed to init helm action config: %w", err)
	}

	installClient := action.NewInstall(actionCfg)
	installClient.ReleaseName = cfg.ReleaseName
	installClient.Namespace = cfg.Namespace
	installClient.RepoURL = cfg.RepoURL
	installClient.WaitStrategy = kube.HookOnlyStrategy
	installClient.Version = cfg.Version

	settings := cli.New()
	chartPath, err := installClient.LocateChart(cfg.ChartName, settings)
	if err != nil {
		return fmt.Errorf("failed to locate chart %s: %w", cfg.ChartName, err)
	}

	chart, err := loader.Load(chartPath)
	if err != nil {
		return fmt.Errorf("failed to load chart %s: %w", cfg.ChartName, err)
	}

	_, err = installClient.RunWithContext(ctx, chart, cfg.Values)
	if err != nil {
		return fmt.Errorf("failed to install chart %s: %w", cfg.ChartName, err)
	}

	return nil
}

// InstallCiliumChart installs the Cilium Helm chart.
func InstallCiliumChart(ctx context.Context, chartVersion string, values map[string]any, kubeconfigContent, ciliumNamespace string) error {
	return InstallHelmChart(ctx, HelmChartConfig{
		ReleaseName: "cilium",
		RepoURL:     "https://helm.cilium.io/",
		ChartName:   "cilium",
		Version:     chartVersion,
		Namespace:   ciliumNamespace,
		Values:      values,
	}, kubeconfigContent)
}
