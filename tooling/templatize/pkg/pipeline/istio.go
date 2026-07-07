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
	"os"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-Tools/pipelines/graph"
	"github.com/Azure/ARO-Tools/pipelines/types"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/istio"
)

func configString(val any) string {
	if s, ok := val.(string); ok {
		return s
	}
	if val == nil {
		return ""
	}
	return fmt.Sprintf("%v", val)
}

func stopAfterForPhase(phase string) (istio.StopAfter, error) {
	switch phase {
	case "install":
		return istio.StopAfterCanaryStart, nil
	case "upgrade", "":
		return "", nil
	default:
		return "", fmt.Errorf("unknown IstioUpgrade phase %q: must be \"install\", \"upgrade\", or empty", phase)
	}
}

func runIstioUpgradeStep(id graph.Identifier, step *types.IstioUpgradeStep, ctx context.Context, options *StepRunOptions, executionTarget ExecutionTarget) error {
	logger := logr.FromContextOrDiscard(ctx).WithValues("stepID", id)

	kubeconfigFile, err := KubeConfig(ctx, executionTarget.GetSubscriptionID(), executionTarget.GetResourceGroup(), step.AKSCluster)
	if err != nil {
		return fmt.Errorf("failed to prepare kubeconfig: %w", err)
	}
	if kubeconfigFile == "" {
		return fmt.Errorf("kubeconfig resolved to empty path for cluster %s", step.AKSCluster)
	}
	defer func() {
		if err := os.Remove(kubeconfigFile); err != nil {
			logger.V(5).Error(err, "failed to delete kubeconfig file", "kubeconfig", kubeconfigFile)
		}
	}()

	versions, err := options.Configuration.GetByPath("svc.istio.versions")
	if err != nil {
		return fmt.Errorf("failed to read svc.istio.versions from config: %w", err)
	}

	tag, err := options.Configuration.GetByPath("svc.istio.tag")
	if err != nil {
		return fmt.Errorf("failed to read svc.istio.tag from config: %w", err)
	}
	ipName, err := options.Configuration.GetByPath("svc.istio.ingressGatewayIPAddressName")
	if err != nil {
		return fmt.Errorf("failed to read svc.istio.ingressGatewayIPAddressName from config: %w", err)
	}
	regionRG, err := options.Configuration.GetByPath("regionRG")
	if err != nil {
		return fmt.Errorf("failed to read regionRG from config: %w", err)
	}

	aksClient, err := istio.NewAKSClient(executionTarget.GetSubscriptionID(), logger, istio.DefaultAKSClientConfig())
	if err != nil {
		return fmt.Errorf("failed to create AKS client: %w", err)
	}

	kubeClient, err := istio.NewKubeClient(kubeconfigFile)
	if err != nil {
		return fmt.Errorf("failed to create kube client: %w", err)
	}

	opts := istio.DefaultUpgradeOptions()
	opts.ResourceGroup = executionTarget.GetResourceGroup()
	opts.ClusterName = step.AKSCluster
	opts.KubeconfigPath = kubeconfigFile
	opts.Versions = configString(versions)
	opts.Tag = configString(tag)
	opts.IngressIPName = configString(ipName)
	opts.RegionRG = configString(regionRG)
	opts.DryRun = step.DryRun

	stopAfter, err := stopAfterForPhase(step.Phase)
	if err != nil {
		return err
	}
	opts.StopAfter = stopAfter

	return istio.RunUpgrade(ctx, opts, aksClient, kubeClient)
}
