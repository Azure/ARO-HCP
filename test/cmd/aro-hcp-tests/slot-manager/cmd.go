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

package slotmanager

import (
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/pkg/logger"
)

func NewCommand() (*cobra.Command, error) {
	var logVerbosity int

	cmd := &cobra.Command{
		Use:           "slot-manager",
		Aliases:       []string{"slot-mgr"},
		Short:         "Manage ARO-HCP E2E slots and related artifacts.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.PersistentFlags().IntVarP(&logVerbosity, "verbosity", "v", 0, "set the verbosity level")
	cmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		ctx := logr.NewContext(cmd.Context(), logger.NewWithVerbosity(logVerbosity))
		cmd.SetContext(ctx)
	}

	registry, err := newAssetRegistry()
	if err != nil {
		return nil, err
	}
	acquireCommand, err := newAcquireCommand(registry)
	if err != nil {
		return nil, err
	}
	releaseCommand, err := newReleaseCommand()
	if err != nil {
		return nil, err
	}
	syncBoskosConfigCommand, err := newSyncBoskosConfigCommand()
	if err != nil {
		return nil, err
	}
	validateBoskosConfigCommand, err := newValidateBoskosConfigCommand()
	if err != nil {
		return nil, err
	}
	applyIdentityPoolCommand, err := newIdentityPoolCompatibilityCommand(registry, false)
	if err != nil {
		return nil, err
	}
	validateIdentityPoolCommand, err := newIdentityPoolCompatibilityCommand(registry, true)
	if err != nil {
		return nil, err
	}
	applyPoolAssetsCommand, err := newApplyPoolAssetsCommand(registry)
	if err != nil {
		return nil, err
	}
	validatePoolAssetsCommand, err := newValidatePoolAssetsCommand(registry)
	if err != nil {
		return nil, err
	}

	cmd.AddCommand(acquireCommand)
	cmd.AddCommand(releaseCommand)
	cmd.AddCommand(syncBoskosConfigCommand)
	cmd.AddCommand(validateBoskosConfigCommand)
	cmd.AddCommand(applyIdentityPoolCommand)
	cmd.AddCommand(validateIdentityPoolCommand)
	cmd.AddCommand(applyPoolAssetsCommand)
	cmd.AddCommand(validatePoolAssetsCommand)
	return cmd, nil
}
