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
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/tools/istio-upgrade/pkg/istio"
)

func NewCommand() (*cobra.Command, error) {
	opts := DefaultOptions()
	cmd := &cobra.Command{
		Use:           "run",
		Short:         "Run an Istio upgrade against an AKS cluster",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}

			ctx := cmd.Context()
			logger := logr.FromContextOrDiscard(ctx)
			upgradeOpts := opts.ToUpgradeOptions()

			aksClient, err := istio.NewAKSClient(opts.SubscriptionID, logger, istio.DefaultAKSClientConfig())
			if err != nil {
				return err
			}

			kubeClient, err := istio.NewKubeClient(opts.KubeconfigPath)
			if err != nil {
				return err
			}

			return istio.RunUpgrade(ctx, upgradeOpts, aksClient, kubeClient)
		},
	}
	if err := BindOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}
