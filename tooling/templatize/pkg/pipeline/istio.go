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

	"github.com/Azure/ARO-Tools/tools/istio-upgrade/pkg/istio"
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

func runIstioUpgradeStep(id graph.Identifier, step *types.IstioUpgradeStep, ctx context.Context, options *StepRunOptions, executionTarget ExecutionTarget, state *ExecutionState) error {
	logger := logr.FromContextOrDiscard(ctx).WithValues("stepID", id)

	state.RLock()
	outputs := state.GetOutputs(id.Stamp)
	state.RUnlock()

	clusterName, err := resolveValue(step.AKSCluster, options.Configuration, outputs, id.ServiceGroup)
	if err != nil {
		return fmt.Errorf("failed to resolve aksCluster: %w", err)
	}
	if clusterName == "" {
		return fmt.Errorf("aksCluster resolved to an empty value")
	}

	kubeconfigFile, err := KubeConfig(ctx, executionTarget.GetSubscriptionID(), executionTarget.GetResourceGroup(), clusterName)
	if err != nil {
		return fmt.Errorf("failed to prepare kubeconfig: %w", err)
	}
	if kubeconfigFile == "" {
		return fmt.Errorf("kubeconfig resolved to empty path for cluster %s", clusterName)
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
	opts.ClusterName = clusterName
	opts.Versions = configString(versions)
	opts.Tag = configString(tag)
	opts.IngressIPName = configString(ipName)
	opts.RegionRG = configString(regionRG)

	return istio.RunUpgrade(ctx, opts, aksClient, kubeClient)
}
