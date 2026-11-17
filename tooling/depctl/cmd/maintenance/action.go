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

package maintenance

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge/ghapi"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/orchestration"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
)

// newCompilerVersionsCommand reads metadata without running target-tree code.
func newCompilerVersionsCommand() *cobra.Command {
	ref := ""
	command := &cobra.Command{Use: "compiler-versions", Short: "Read compiled workflow release provenance", Args: cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			versions, err := orchestration.CompilerVersions(cmd.Context(), ref, orchestration.RunGit)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(versions)
		}}
	command.Flags().StringVar(&ref, "ref", "", "git revision containing compiled workflows")
	return command
}

// newActionCommand binds one fixed-command guarded action.
func newActionCommand(mode string) *cobra.Command {
	action := orchestration.MaintenanceAction{Mode: mode}
	command := &cobra.Command{Use: mode, Short: "Assess current-head evidence and enforce maintenance policy", Args: cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := policy.ValidateMaintenanceTarget(action.Repository, action.Number, action.Head); err != nil {
				return cli.Invalid(err)
			}
			if _, err := policy.MaintenanceKinds(action.Kind); err != nil {
				return cli.Invalid(err)
			}
			reader, err := ghapi.NewMaintenance(action.Repository)
			if err != nil {
				return err
			}
			engine := orchestration.New(orchestration.Deps{Forge: reader})
			decision, id, err := engine.ExecuteMaintenance(cmd.Context(), action, reader)
			if err != nil {
				return err
			}
			record := inventoryPayload([]model.MaintenanceDecision{decision})[0]
			record.CancellationCommentID = id
			return json.NewEncoder(cmd.OutOrStdout()).Encode(record)
		}}
	command.Flags().StringVar(&action.Repository, "repository", "", "OWNER/REPO")
	command.Flags().IntVar(&action.Number, "number", 0, "pull request number")
	command.Flags().StringVar(&action.Head, "head", "", "expected full lowercase head SHA")
	command.Flags().StringVar(&action.Kind, "kind", "", "upgrade, dependency, tool, or non-compiler")
	command.Flags().StringVar(&action.Evidence, "evidence", "", "diagnosed failure and trusted comparison evidence")
	command.Flags().StringVar(&action.PreviousHead, "previous-head", "", "revision before compiler regeneration")
	return command
}
