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

package keyvault

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/sets"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/common"
)

// ResourceGroupExistsFn reports whether a resource group still exists in the
// subscription. It is injected so the step can be unit-tested without Azure.
type ResourceGroupExistsFn func(ctx context.Context, resourceGroupName string) (bool, error)

// PurgeOrphanedDeletedStepConfig configures orphaned deleted Key Vault purge behavior.
//
// Unlike PurgeDeletedStep, which is scoped to a single (still-existing) resource
// group, this step operates subscription-wide and only targets soft-deleted
// vaults whose backing resource group has already been deleted. Deleting a
// resource group only soft-deletes its Key Vaults, and Azure keeps the globally
// unique vault name reserved for the soft-delete retention window (up to 90
// days). Because names derived deterministically from the resource group id
// (e.g. the e2e customer vault cust-kv-${uniqueString(resourceGroup().id, ...)})
// would then collide with VaultAlreadyExists on a later run, these orphans must
// be purged even though no resource group remains for the rg-ordered workflow to
// sweep.
type PurgeOrphanedDeletedStepConfig struct {
	VaultsClient        *armkeyvault.VaultsClient
	ResourceGroupExists ResourceGroupExistsFn

	Name            string
	Retries         int
	ContinueOnError bool
	Verify          runner.VerifyFn
}

type purgeOrphanedDeletedStep struct {
	cfg             PurgeOrphanedDeletedStepConfig
	name            string
	retries         int
	continueOnError bool
	verify          runner.VerifyFn
}

var _ runner.Step = (*purgeOrphanedDeletedStep)(nil)

// NewPurgeOrphanedDeletedStep builds the orphaned deleted Key Vault purge step.
func NewPurgeOrphanedDeletedStep(cfg PurgeOrphanedDeletedStepConfig) (runner.Step, error) {
	if cfg.VaultsClient == nil {
		return nil, fmt.Errorf("vaults client is required")
	}
	if cfg.ResourceGroupExists == nil {
		return nil, fmt.Errorf("resource group existence check is required")
	}

	stepName := cfg.Name
	if strings.TrimSpace(stepName) == "" {
		stepName = "Purge orphaned soft-deleted Key Vaults"
	}

	return &purgeOrphanedDeletedStep{
		cfg:             cfg,
		name:            stepName,
		retries:         cfg.Retries,
		continueOnError: cfg.ContinueOnError,
		verify:          cfg.Verify,
	}, nil
}

// MustNewPurgeOrphanedDeletedStep builds the step and panics on invalid config.
func MustNewPurgeOrphanedDeletedStep(cfg PurgeOrphanedDeletedStepConfig) runner.Step {
	step, err := NewPurgeOrphanedDeletedStep(cfg)
	if err != nil {
		panic(err)
	}
	return step
}

func (s *purgeOrphanedDeletedStep) Name() string {
	return s.name
}

func (s *purgeOrphanedDeletedStep) RetryLimit() int {
	if s.retries < runner.DefaultRetries {
		return runner.DefaultRetries
	}
	return s.retries
}

func (s *purgeOrphanedDeletedStep) ContinueOnError() bool {
	return s.continueOnError
}

func (s *purgeOrphanedDeletedStep) Verify(ctx context.Context) error {
	if s.verify == nil {
		return nil
	}
	return s.verify(ctx)
}

func (s *purgeOrphanedDeletedStep) Discover(ctx context.Context) ([]runner.Target, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		panic(err)
	}
	skipReporter := common.NewDiscoverySkipReporter(s.Name())
	defer skipReporter.Flush(logger)

	// Cache resource-group existence lookups so subscriptions with many
	// soft-deleted vaults sharing a resource group only pay one API call each.
	checkedRGs := sets.New[string]()
	existingRGs := sets.New[string]()

	pager := s.cfg.VaultsClient.NewListDeletedPager(nil)
	targets := []runner.Target{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list deleted vaults: %w", err)
		}
		for i, vault := range page.Value {
			if vault == nil || vault.Name == nil || vault.Properties == nil || vault.Properties.Location == nil || vault.Properties.VaultID == nil {
				skipReporter.Record(logger, "invalid_deleted_vault_payload", "index", i)
				continue
			}
			vaultID := *vault.Properties.VaultID
			parsed, err := azcorearm.ParseResourceID(vaultID)
			if err != nil {
				// If we cannot determine the owning resource group we cannot
				// prove the vault is orphaned, so skip it rather than risk
				// purging a vault whose resource group still exists.
				skipReporter.Record(logger, "unparseable_vault_id", "vault", *vault.Name, "vaultID", vaultID, "error", err)
				continue
			}
			resourceGroupName := parsed.ResourceGroupName
			if resourceGroupName == "" {
				// A parsed ID without a resource group is not an RG-scoped ARM ID,
				// so we cannot prove the vault is orphaned. Skip it rather than
				// probing an empty resource group name (which would fail and cache
				// unrelated malformed IDs under one empty-string key).
				skipReporter.Record(logger, "vault_id_missing_resource_group", "vault", *vault.Name)
				continue
			}

			rgKey := strings.ToLower(resourceGroupName)
			if !checkedRGs.Has(rgKey) {
				checkedRGs.Insert(rgKey)
				exists, err := s.cfg.ResourceGroupExists(ctx, resourceGroupName)
				if err != nil {
					// Be conservative on lookup failure: treat the resource group
					// as existing so we skip this and any sibling vaults without
					// re-running CheckExistence (avoids amplifying API load / log
					// noise under transient ARM throttling). A later sweep retries.
					existingRGs.Insert(rgKey)
					skipReporter.Record(logger, "resource_group_existence_check_failed", "vault", *vault.Name, "resourceGroup", resourceGroupName, "error", err)
					continue
				}
				if exists {
					existingRGs.Insert(rgKey)
				}
			}
			if existingRGs.Has(rgKey) {
				// Resource group still exists; the rg-ordered workflow owns
				// purging vaults in live resource groups.
				continue
			}

			targets = append(targets, runner.Target{
				ID:       vaultID,
				Name:     *vault.Name,
				Type:     DeletedVaultsResourceType,
				Location: *vault.Properties.Location,
			})
		}
	}
	return targets, nil
}

func (s *purgeOrphanedDeletedStep) Delete(ctx context.Context, target runner.Target, wait bool) error {
	if target.Location == "" {
		return fmt.Errorf("missing location for vault %s", target.Name)
	}
	return purgeDeletedVault(ctx, s.cfg.VaultsClient, target.Name, target.Location, wait)
}
