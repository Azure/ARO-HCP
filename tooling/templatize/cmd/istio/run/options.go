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

package run

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/tools/istio-upgrade/pkg/istio"
)

type RawRunOptions struct {
	SubscriptionID string
	ResourceGroup  string
	ClusterName    string
	KubeconfigPath string
	Versions       string
	Tag            string
	IngressIPName  string
	RegionRG       string
	DryRun         bool
	StopAfter      string
}

func DefaultOptions() *RawRunOptions {
	return &RawRunOptions{
		DryRun: true,
	}
}

func BindOptions(opts *RawRunOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.SubscriptionID, "subscription-id", opts.SubscriptionID, "Azure subscription ID")
	cmd.Flags().StringVar(&opts.ResourceGroup, "resource-group", opts.ResourceGroup, "Azure resource group containing the AKS cluster")
	cmd.Flags().StringVar(&opts.ClusterName, "cluster-name", opts.ClusterName, "AKS cluster name")
	cmd.Flags().StringVar(&opts.KubeconfigPath, "kubeconfig", opts.KubeconfigPath, "path to kubeconfig file")
	cmd.Flags().StringVar(&opts.Versions, "versions", opts.Versions, "target Istio revision (e.g. asm-1-29)")
	cmd.Flags().StringVar(&opts.Tag, "tag", opts.Tag, "revision tag name to flip (e.g. 'default')")
	cmd.Flags().StringVar(&opts.IngressIPName, "ingress-ip-name", opts.IngressIPName, "public IP name for ingress gateway annotation")
	cmd.Flags().StringVar(&opts.RegionRG, "region-rg", opts.RegionRG, "resource group containing the ingress public IP")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", opts.DryRun, "log the decision without mutating the cluster")
	cmd.Flags().StringVar(&opts.StopAfter, "stop-after", opts.StopAfter, "halt upgrade at a phase for resume testing (canary-start, orphan-check)")
	return nil
}

func (o *RawRunOptions) Validate() error {
	if o.SubscriptionID == "" {
		return fmt.Errorf("--subscription-id is required")
	}
	if o.ResourceGroup == "" {
		return fmt.Errorf("--resource-group is required")
	}
	if o.ClusterName == "" {
		return fmt.Errorf("--cluster-name is required")
	}
	if o.KubeconfigPath == "" {
		return fmt.Errorf("--kubeconfig is required")
	}
	if o.Versions == "" {
		return fmt.Errorf("--versions is required")
	}
	if o.StopAfter != "" {
		if _, err := istio.ValidateStopAfter(o.StopAfter); err != nil {
			return err
		}
	}
	return nil
}

func (o *RawRunOptions) ToUpgradeOptions() istio.UpgradeOptions {
	opts := istio.DefaultUpgradeOptions()
	opts.ResourceGroup = o.ResourceGroup
	opts.ClusterName = o.ClusterName
	opts.Versions = o.Versions
	opts.Tag = o.Tag
	opts.IngressIPName = o.IngressIPName
	opts.RegionRG = o.RegionRG
	opts.DryRun = o.DryRun
	if o.StopAfter != "" {
		opts.StopAfter = istio.StopAfter(o.StopAfter)
	}
	return opts
}
