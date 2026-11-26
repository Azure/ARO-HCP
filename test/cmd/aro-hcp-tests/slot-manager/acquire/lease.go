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

package acquire

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/catalog"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/clusterprofile"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/lease"
)

// finalizeAcquiredLease turns a primary lease into a published slot: it records
// the lease durably first, then resolves the slot and subscriptions, acquires and
// admits assets, and writes the runtime contract. Any failure rolls back every
// lease recorded so far.
func (o *runner) finalizeAcquiredLease(ctx context.Context, pool catalog.Pool, leasedName string) (result error) {
	if err := lease.ValidateLeasedResourceName(leasedName); err != nil {
		return fmt.Errorf("primary lease acquisition returned an invalid name for type %q: %w", pool.ResourceType, err)
	}
	state := &lease.AcquiredSlotState{
		Version:            2,
		LeasedResourceName: leasedName,
		Leases:             lease.LeaseSet{Primary: lease.Lease{ResourceType: pool.ResourceType, ResourceName: leasedName}},
	}
	writeState := o.WriteState
	if writeState == nil {
		writeState = lease.WriteAcquiredSlotState
	}
	journal := lease.NewJournal(state, o.SharedDir, o.LeaseProxyURL, o.LeaseProxyTimeout)
	journal.Persist = func() error { return writeState(o.SharedDir, state) }
	defer func() {
		if result == nil {
			return
		}
		// Acquisition's writer may have failed. Retry durable cleanup with the
		// standard writer; never return a recorded resource without a journal.
		journal.Persist = func() error { return lease.WriteAcquiredSlotState(o.SharedDir, state) }
		cleanupErr := o.AssetRegistry.ReleaseLease(ctx, assets.LeaseRequest{AcquiredSlotState: state, LeaseJournal: journal})
		if cleanupErr == nil {
			cleanupErr = lease.RemoveStateFiles(o.SharedDir)
		}
		result = errors.Join(result, cleanupErr)
	}()
	// Write the primary immediately: even slot/profile resolution can fail.
	if err := journal.Persist(); err != nil {
		return err
	}
	logger := logr.FromContextOrDiscard(ctx).WithValues(
		"slotName", leasedName,
		"environment", o.PoolEnvironment,
		"pool", describePool(pool),
	)
	logger.Info("Acquired primary slot lease")
	slot, err := o.ResolveLeasedSlot(pool, leasedName)
	if err != nil {
		return err
	}
	state.Slot = *slot
	state.DeployEnvironment = pool.DeployEnv
	state.RuntimeRegion = o.RuntimeRegionForPool(pool)
	customerSubscription, profile, err := clusterprofile.VerifyCustomerSubscriptionName(o.ClusterProfileDirs, pool.Subscriptions.E2E)
	if err != nil {
		return err
	}
	if o.DeployEnvironment != "" && o.DeployEnvironment != pool.DeployEnv {
		return fmt.Errorf("acquired deploy environment %q does not match requested %q", pool.DeployEnv, o.DeployEnvironment)
	}
	state.Slot.Subscriptions, err = o.ResolveSubscriptions(ctx, profile, pool.DeployEnv, pool.Subscriptions.E2E, pool.InfrastructureSubscriptionName())
	if err != nil {
		return err
	}
	request := assets.LeaseRequest{AcquiredSlotState: state, SelectedClusterProfileDir: profile, LeaseJournal: journal}
	if err := o.AssetRegistry.AcquireLeases(ctx, request, o.AssetInventories); err != nil {
		return err
	}
	if err := journal.Persist(); err != nil {
		return err
	}
	if err := state.ValidateForAdmission(); err != nil {
		return err
	}
	logger.Info("Starting asset admission",
		"assetCount", len(state.Slot.AssetRequirements),
		"runtimeRegion", state.RuntimeRegion,
		"timeout", o.AdmissionTimeout,
	)
	admissionCtx, cancelAdmission := context.WithTimeout(ctx, o.AdmissionTimeout)
	err = o.AssetRegistry.AdmitLease(admissionCtx, request, o.DisabledAssetAdmission...)
	if err == nil {
		err = admissionCtx.Err()
	}
	cancelAdmission()
	if err != nil {
		return err
	}
	if err := state.ValidateForPublication(); err != nil {
		return err
	}
	if err := journal.Persist(); err != nil {
		return err
	}
	contract := lease.NewRuntimeContractBuilder()
	if err := lease.AddCoreRuntimeExports(contract, state, customerSubscription, profile); err != nil {
		return err
	}
	if err := o.AssetRegistry.PublishLease(ctx, request, contract); err != nil {
		return err
	}
	if err := lease.WriteRuntimeContract(o.SharedDir, contract); err != nil {
		return err
	}
	logger.Info("Acquired slot and wrote shared artifacts",
		"disabledAssetAdmission", o.DisabledAssetAdmission,
		"regionMode", o.RegionSelection.Mode,
		"catalogRegions", strings.Join(o.RegionSelection.CatalogRegions, ","),
		"locationWeights", strings.Join(o.RegionSelection.NormalizedWeights, ","),
		"locationOverrideUsed", o.RegionSelection.LocationOverrideUsed,
		"selectionKeySource", o.RegionSelection.SelectionKeySource,
		"runtimeRegion", state.RuntimeRegion,
		"sharedDir", o.SharedDir,
	)
	return nil
}
