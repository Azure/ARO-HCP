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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge/ghapi"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/orchestration"
)

// inventoryReaderFactory builds the read-only proposal reader.
var inventoryReaderFactory = func(repository string) (forge.Reader, error) {
	return ghapi.New(repository)
}

// InventoryOptions are the unvalidated inventory flags.
type InventoryOptions struct {
	Repository string
	Output     string

	out io.Writer
	err io.Writer
}

// DefaultInventoryOptions returns the raw inventory options.
func DefaultInventoryOptions() *InventoryOptions {
	return &InventoryOptions{}
}

// BindInventoryOptions registers the inventory flags.
func BindInventoryOptions(options *InventoryOptions, command *cobra.Command) error {
	command.Flags().StringVar(&options.Repository, "repository", options.Repository, "OWNER/REPO")
	command.Flags().StringVar(&options.Output, "output", options.Output,
		"write the inventory array here instead of stdout")
	return nil
}

// ValidatedInventory are the inventory options that passed validation.
type ValidatedInventory struct {
	*InventoryOptions
}

// Validate checks the inventory inputs.
func (o *InventoryOptions) Validate(_ context.Context) (*ValidatedInventory, error) {
	if o.Repository == "" {
		return nil, cli.Invalid(errors.New("--repository OWNER/REPO is required"))
	}
	return &ValidatedInventory{InventoryOptions: o}, nil
}

// CompletedInventory are the inventory options with their engine.
type CompletedInventory struct {
	*ValidatedInventory
	engine *orchestration.Engine
}

// Complete builds the read-only engine.
func (v *ValidatedInventory) Complete(_ context.Context) (*CompletedInventory, error) {
	reader, err := inventoryReaderFactory(v.Repository)
	if err != nil {
		if errors.Is(err, forge.ErrAuthentication) {
			return nil, cli.Incomplete("read owned proposals: %v", err)
		}
		return nil, cli.Invalid(err)
	}
	return &CompletedInventory{
		ValidatedInventory: v,
		engine:             orchestration.New(orchestration.Deps{Forge: reader}),
	}, nil
}

// Run assesses every owned proposal and emits the inventory array.
func (c *CompletedInventory) Run(ctx context.Context) error {
	decisions, err := c.engine.MaintenanceInventory(ctx, c.Repository)
	if err != nil {
		return cli.MapEngineError(err)
	}
	encoded, err := json.MarshalIndent(inventoryPayload(decisions), "", "  ")
	if err != nil {
		return cli.Internal(err)
	}
	encoded = append(encoded, '\n')
	if c.Output == "" {
		_, err := c.out.Write(encoded)
		return err
	}
	if err := os.WriteFile(c.Output, encoded, 0o600); err != nil {
		return cli.Internal(fmt.Errorf("write inventory: %w", err))
	}
	fmt.Fprintf(c.err, "assessed %d owned proposals\n", len(decisions))
	return nil
}

// inventoryPayload converts decisions to the snake_case array the agent reads.
func inventoryPayload(decisions []model.MaintenanceDecision) []map[string]any {
	payload := make([]map[string]any, 0, len(decisions))
	for _, decision := range decisions {
		holds := make([]map[string]any, 0, len(decision.ActiveHolds))
		for _, hold := range decision.ActiveHolds {
			var sha any
			if hold.SHA != "" {
				sha = hold.SHA
			}
			holds = append(holds, map[string]any{
				"comment_id": hold.CommentID, "author": hold.Author, "kind": hold.Kind, "sha": sha,
			})
		}
		blocked := make([]map[string]any, 0, len(decision.CI.Blocked))
		for _, check := range decision.CI.Blocked {
			blocked = append(blocked, map[string]any{
				"name": check.Name, "state": check.State, "url": check.URL, "pending": check.Pending,
			})
		}
		payload = append(payload, map[string]any{
			"repository": decision.Repository, "pull_request": decision.PullRequest,
			"expected_head": decision.ExpectedHead, "head": decision.Head, "kind": decision.Kind,
			"held": decision.Held, "active_holds": holds,
			"ci":             map[string]any{"positive": nonNilStrings(decision.CI.Positive), "blocked": blocked},
			"release_status": decision.ReleaseStatus, "can_release_hold": decision.CanReleaseHold,
			"can_recover_retry": decision.CanRecoverRetry, "retry_blockers": nonNilStrings(decision.RetryBlockers),
			"recovery_marker": decision.RecoveryMarker, "can_refresh_upgrade": decision.CanRefreshUpgrade,
			"release_blockers": nonNilStrings(decision.ReleaseBlockers),
			"refresh_blockers": nonNilStrings(decision.RefreshBlockers),
		})
	}
	return payload
}

// nonNilStrings keeps an empty collection as an empty array.
func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
