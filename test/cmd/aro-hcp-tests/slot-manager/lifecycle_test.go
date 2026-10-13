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
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

const lifecycleCatalog = `version: 2
asset_pools:
- name: bundles
  kind: infrastructure_identities
  boskos_resource_type: bundle-type
  resource_name_prefix: bundle
environments:
  dev:
    deployment_environment: {name: ci01, infrastructure_subscription: dev-infra}
    pools:
    - name: shard0
      region: westus3
      slot_count: 1
      subscriptions: {e2e: dev-e2e}
      slot_assets:
        e2e_identities:
          allocation: dedicated
          resource_group_prefix: identities
          resource_group_count: 1
        infrastructure_identities:
          allocation: leased
          asset_pool: bundles
          units_per_slot: 2
  other:
    deployment_environment: {name: other-ci, infrastructure_subscription: dev-infra}
    pools:
    - name: shard1
      region: centralus
      slot_count: 3
      subscriptions: {e2e: other-e2e}
      slot_assets:
        e2e_identities:
          allocation: dedicated
          resource_group_prefix: other-identities
          resource_group_count: 1
        infrastructure_identities:
          allocation: leased
          asset_pool: bundles
`

type lifecycleHandler struct {
	kind             slots.AssetKind
	calls            *[]string
	fail             string
	before           func(string, assets.LeaseRequest)
	admit            func(context.Context) error
	assetInventories *[]slots.AssetInventory
	poolRequests     *[]assets.PoolRequest
}

func (h *lifecycleHandler) Kind() assets.Kind { return h.kind }
func (h *lifecycleHandler) Declared(pool slots.Pool) bool {
	for _, assetRequirement := range pool.Requirements() {
		if assetRequirement.Kind == h.kind {
			return true
		}
	}
	return false
}
func (h *lifecycleHandler) call(phase string, request assets.LeaseRequest) error {
	*h.calls = append(*h.calls, phase+":"+string(h.kind))
	if h.before != nil {
		h.before(phase, request)
	}
	if h.fail == phase {
		return fmt.Errorf("fake %s failed", phase)
	}
	return nil
}
func (h *lifecycleHandler) AcquireLease(_ context.Context, request assets.LeaseRequest) error {
	if err := h.call("resolve", request); err != nil {
		return err
	}
	if h.kind == slots.KindInfrastructureIdentities {
		resolved := &slots.ResolvedInfrastructureIdentities{Allocation: slots.AllocationLeased, Identities: []string{"fake-service", "fake-management"}}
		for _, lease := range request.AcquiredSlotState.Leases.Assets[h.kind] {
			resolved.ResourceGroups = append(resolved.ResourceGroups, lease.ResourceName)
		}
		request.AcquiredSlotState.Slot.Assets.InfrastructureIdentities = resolved
	}
	return nil
}
func (h *lifecycleHandler) ReleaseLease(ctx context.Context, request assets.LeaseRequest) error {
	*h.calls = append(*h.calls, "release:"+string(h.kind))
	return request.LeaseJournal.ReleaseAsset(ctx, h.kind)
}
func (h *lifecycleHandler) ApplyPools(_ context.Context, request assets.PoolRequest) error {
	*h.calls = append(*h.calls, "apply:"+string(h.kind))
	if h.assetInventories != nil {
		*h.assetInventories = request.AssetInventories
	}
	if h.poolRequests != nil {
		*h.poolRequests = append(*h.poolRequests, request)
	}
	return nil
}
func (h *lifecycleHandler) ValidatePools(ctx context.Context, request assets.PoolRequest) error {
	return h.ApplyPools(ctx, request)
}
func (h *lifecycleHandler) AdmitLease(ctx context.Context, request assets.LeaseRequest) error {
	if h.admit != nil {
		if err := h.admit(ctx); err != nil {
			return err
		}
	}
	return h.call("admit", request)
}
func (h *lifecycleHandler) PublishLease(_ context.Context, request assets.LeaseRequest, contract *slots.RuntimeContractBuilder) error {
	if err := h.call("publish", request); err != nil {
		return err
	}
	return contract.Add(string(h.kind), "FAKE_"+string(h.kind), "ready")
}

func lifecycleOptions(t *testing.T, catalog, server string, registry *assets.Registry) *RawAcquireOptions {
	t.Helper()
	return &RawAcquireOptions{
		ClusterProfileDir:   writeAcquireTestClusterProfile(t, "dev-e2e"),
		Environment:         "dev",
		SharedDir:           t.TempDir(),
		CatalogPath:         writeAcquireTestCatalogFromYAML(t, catalog),
		LeaseProxyServerURL: server,
		LeaseProxyTimeout:   100 * time.Millisecond,
		MaxWaitForLease:     0,
		LeaseWaitInterval:   time.Millisecond,
		AssetRegistry:       registry,
		ResolveSubscriptions: func(context.Context, string, string, string, string) (slots.ResolvedSubscriptions, error) {
			return slots.ResolvedSubscriptions{
				E2E:            slots.ResolvedSubscription{Name: "dev-e2e", ID: "e2e-id"},
				Infrastructure: slots.ResolvedSubscription{Name: "dev-infra", ID: "infra-id"},
			}, nil
		},
	}
}

func TestAcquireAdmissionDeadlineAndRollback(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"default", "extended", "expired", "expired without handler error"} {
		t.Run(scenario, func(t *testing.T) {
			var calls []string
			var firstDeadline time.Time
			wantTimeout := DefaultAdmissionTimeout
			if scenario == "extended" {
				wantTimeout = 30 * time.Minute
			}
			expires := strings.HasPrefix(scenario, "expired")
			if expires {
				wantTimeout = 20 * time.Millisecond
			}
			e2e := &lifecycleHandler{kind: slots.KindE2EIdentities, calls: &calls}
			infra := &lifecycleHandler{kind: slots.KindInfrastructureIdentities, calls: &calls}
			e2e.admit = func(ctx context.Context) error {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > wantTimeout || (!expires && time.Until(deadline) < wantTimeout-time.Second) {
					t.Fatalf("handler received incorrect deadline: %v, expected budget %v", deadline, wantTimeout)
				}
				firstDeadline = deadline
				return nil
			}
			infra.admit = func(ctx context.Context) error {
				deadline, ok := ctx.Deadline()
				if !ok || !deadline.Equal(firstDeadline) {
					t.Fatal("all handlers must share one admission deadline")
				}
				if expires {
					<-ctx.Done()
					if scenario == "expired" {
						return ctx.Err()
					}
				}
				return nil
			}
			registry, err := assets.NewRegistry(e2e, infra)
			if err != nil {
				t.Fatal(err)
			}
			server, _, released := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
				"aro-hcp-dev-shard0-slot": {successAcquireReply("aro-hcp-dev-shard0-slot-00")},
				"bundle-type":             {successAcquireReply("bundle-00"), successAcquireReply("bundle-01")},
			})
			defer server.Close()
			options := lifecycleOptions(t, lifecycleCatalog, server.URL, registry)
			if scenario != "default" {
				options.AdmissionTimeout = wantTimeout.String()
			}
			err = Acquire(t.Context(), options)
			if expires {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("expected admission deadline failure, got %v", err)
				}
				for _, call := range calls {
					if strings.HasPrefix(call, "publish:") {
						t.Fatalf("published after expired admission: %v", calls)
					}
				}
				env, _ := slots.EnvFile(options.SharedDir)
				if _, err := os.Stat(env); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("runtime exports exist after timeout: %v", err)
				}
			} else {
				if err != nil {
					t.Fatalf("successful admission must still publish after cancelling its own context: %v", err)
				}
				if err := Release(t.Context(), &RawReleaseOptions{SharedDir: options.SharedDir, LeaseProxyServerURL: server.URL, LeaseProxyTimeout: time.Second}); err != nil {
					t.Fatal(err)
				}
			}
			if t.Context().Err() != nil || len(*released) != 3 || (*released)[2] != "aro-hcp-dev-shard0-slot-00" {
				t.Fatalf("admission must not cancel its parent or prevent lease return: %v, %v", t.Context().Err(), *released)
			}
		})
	}
}

func TestIndependentAssetLifecycleAndRollback(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"success", "resolve", "admit", "publish", "second acquire", "duplicate secondary", "unexpected name", "malformed secondary", "timeout", "primary state write", "secondary state write", "subscription resolution", "invalid runtime state", "skip e2e admission", "skip infra admission", "skip both admissions", "unskipped admission fails"} {
		t.Run(scenario, func(t *testing.T) {
			calls := []string{}
			e2e := &lifecycleHandler{kind: slots.KindE2EIdentities, calls: &calls}
			infra := &lifecycleHandler{kind: slots.KindInfrastructureIdentities, calls: &calls}
			skipE2E := scenario == "skip e2e admission" || scenario == "skip both admissions" || scenario == "unskipped admission fails"
			skipInfra := scenario == "skip infra admission" || scenario == "skip both admissions"
			if skipE2E {
				e2e.fail = "admit"
			}
			if skipInfra || scenario == "unskipped admission fails" {
				infra.fail = "admit"
			}
			if scenario == "resolve" || scenario == "admit" || scenario == "publish" {
				infra.fail = scenario
			}
			registry, err := assets.NewRegistry(
				e2e,
				infra,
			)
			if err != nil {
				t.Fatal(err)
			}
			second := successAcquireReply("bundle-01")
			switch scenario {
			case "second acquire":
				second = leaseProxyReply{statusCode: http.StatusForbidden, body: "denied"}
			case "duplicate secondary":
				second = successAcquireReply("bundle-04")
			case "unexpected name":
				second = successAcquireReply("foreign-90")
			case "malformed secondary":
				second = successAcquireReply(" bundle-01 ")
			case "timeout":
				second = delayedLeaseProxyReply(150*time.Millisecond, unavailableAcquireReply("bundle-type"))
			}
			server, acquired, released := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
				"aro-hcp-dev-shard0-slot": {successAcquireReply("aro-hcp-dev-shard0-slot-00")},
				"bundle-type":             {successAcquireReply("bundle-04"), second},
			})
			defer server.Close()
			options := lifecycleOptions(t, lifecycleCatalog, server.URL, registry)
			command := &cobra.Command{}
			if err := BindAcquireOptions(options, command); err != nil {
				t.Fatal(err)
			}
			var flags []string
			if skipE2E {
				flags = append(flags, "--disable-asset-admission=e2e_identities")
			}
			if skipInfra {
				flags = append(flags, "--disable-asset-admission=infrastructure_identities")
			}
			if err := command.ParseFlags(flags); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			logger := funcr.New(func(_, message string) {
				fmt.Fprintln(&logs, message)
				if strings.Contains(message, "Acquired slot and wrote shared artifacts") {
					env, _ := slots.EnvFile(options.SharedDir)
					if _, err := os.Stat(env); err != nil {
						t.Fatalf("success logged before runtime contract was published: %v", err)
					}
				}
			}, funcr.Options{})
			writes := 0
			options.WriteState = func(dir string, state *slots.AcquiredSlotState) error {
				writes++
				if (scenario == "primary state write" && writes == 1) || (scenario == "secondary state write" && len(state.Leases.Assets[slots.KindInfrastructureIdentities]) == 1) {
					return errors.New("state disk write failed")
				}
				return slots.WriteAcquiredSlotState(dir, state)
			}
			resolve := options.ResolveSubscriptions
			options.ResolveSubscriptions = func(ctx context.Context, profile, deploy, e2e, infra string) (slots.ResolvedSubscriptions, error) {
				state, err := slots.LoadAcquiredSlotState(options.SharedDir)
				if err != nil || state.Leases.Primary.ResourceName != "aro-hcp-dev-shard0-slot-00" {
					t.Fatalf("primary not persisted before subscription resolution: %+v, %v", state, err)
				}
				if scenario == "subscription resolution" {
					return slots.ResolvedSubscriptions{}, errors.New("profile binding mismatch")
				}
				if scenario == "invalid runtime state" {
					return slots.ResolvedSubscriptions{}, nil
				}
				return resolve(ctx, profile, deploy, e2e, infra)
			}
			infra.before = func(phase string, request assets.LeaseRequest) {
				state, err := slots.LoadAcquiredSlotState(options.SharedDir)
				if err != nil {
					t.Fatal(err)
				}
				want := []slots.Lease{{ResourceType: "bundle-type", ResourceName: "bundle-04"}, {ResourceType: "bundle-type", ResourceName: "bundle-01"}}
				if !reflect.DeepEqual(state.Leases.Assets[slots.KindInfrastructureIdentities], want) {
					t.Fatalf("%s ran without exact secondary names persisted: %+v", phase, state.Leases)
				}
				if phase == "admit" && request.AcquiredSlotState.Slot.Assets.InfrastructureIdentities == nil {
					t.Fatal("admission ran before resolution")
				}
				if phase == "admit" && (!strings.Contains(logs.String(), "Acquired primary slot lease") || !strings.Contains(logs.String(), "Starting asset admission")) {
					t.Fatalf("missing progress logs before admission: %s", logs.String())
				}
				env, _ := slots.EnvFile(options.SharedDir)
				if _, err := os.Stat(env); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("runtime contract was visible before all admission/publication passed")
				}
			}
			err = Acquire(logr.NewContext(context.Background(), logger), options)
			wantSuccess := scenario == "success" || scenario == "skip e2e admission" || scenario == "skip infra admission" || scenario == "skip both admissions"
			if loggedSuccess := strings.Contains(logs.String(), "Acquired slot and wrote shared artifacts"); loggedSuccess != wantSuccess {
				t.Fatalf("success log does not match acquisition outcome: %s; error: %v", logs.String(), err)
			}
			for kind, skipped := range map[slots.AssetKind]bool{slots.KindE2EIdentities: skipE2E, slots.KindInfrastructureIdentities: skipInfra} {
				loggedSkip := false
				for _, line := range strings.Split(logs.String(), "\n") {
					if strings.Contains(line, "WARNING: asset admission explicitly disabled") && strings.Contains(line, string(kind)) {
						loggedSkip = true
					}
				}
				if loggedSkip != skipped {
					t.Fatalf("admission warning does not match opt-out for %s: %s", kind, logs.String())
				}
			}
			if wantSuccess {
				if err != nil {
					t.Fatalf("acquire failed: %v", err)
				}
				wantCalls := []string{"resolve:e2e_identities", "resolve:infrastructure_identities"}
				if !skipE2E {
					wantCalls = append(wantCalls, "admit:e2e_identities")
				}
				if !skipInfra {
					wantCalls = append(wantCalls, "admit:infrastructure_identities")
				}
				wantCalls = append(wantCalls, "publish:e2e_identities", "publish:infrastructure_identities")
				if !reflect.DeepEqual(calls, wantCalls) {
					t.Fatalf("incorrect admission order: %v", calls)
				}
				env, _ := slots.EnvFile(options.SharedDir)
				data, err := os.ReadFile(env)
				if err != nil || !strings.Contains(string(data), "ARO_HCP_DEPLOY_ENV='ci01'") || !strings.Contains(string(data), "FAKE_infrastructure_identities='ready'") {
					t.Fatalf("incomplete runtime contract: %s, %v", data, err)
				}
				if len(*released) != 0 || len(*acquired) != 3 {
					t.Fatalf("incorrect acquisitions/releases: %v / %v", *acquired, *released)
				}
				if err := Acquire(context.Background(), options); err == nil || !strings.Contains(err.Error(), "already exists") {
					t.Fatalf("second acquisition should not overwrite existing leases: %v", err)
				}
				if err := Release(context.Background(), &RawReleaseOptions{
					SharedDir: options.SharedDir, LeaseProxyServerURL: server.URL, LeaseProxyTimeout: time.Second,
				}); err != nil {
					t.Fatalf("releasing acquired assets: %v", err)
				}
				if want := []string{"bundle-04", "bundle-01", "aro-hcp-dev-shard0-slot-00"}; !reflect.DeepEqual(*released, want) {
					t.Fatalf("release missed acquired assets: got %v want %v", *released, want)
				}
				return
			}
			if err == nil {
				t.Fatal("expected fail-closed acquisition")
			}
			if infra.fail != "" && !strings.Contains(err.Error(), "fake "+infra.fail+" failed") {
				t.Fatalf("expected failure from %s, got %v", infra.fail, err)
			}
			if scenario == "invalid runtime state" {
				if !strings.Contains(err.Error(), "unresolved E2E subscription") {
					t.Fatalf("expected runtime validation failure, got %v", err)
				}
				for _, call := range calls {
					if !strings.HasPrefix(call, "resolve:") && !strings.HasPrefix(call, "release:") {
						t.Fatalf("invalid state reached admission: %v", calls)
					}
				}
			}
			wantReleased := []string{"bundle-04", "bundle-01", "aro-hcp-dev-shard0-slot-00"}
			switch scenario {
			case "primary state write", "subscription resolution":
				wantReleased = []string{"aro-hcp-dev-shard0-slot-00"}
			case "second acquire", "duplicate secondary", "malformed secondary", "timeout", "secondary state write":
				wantReleased = []string{"bundle-04", "aro-hcp-dev-shard0-slot-00"}
			case "unexpected name":
				wantReleased[1] = "foreign-90"
			}
			if !reflect.DeepEqual(*released, wantReleased) {
				t.Fatalf("rollback missed acquired leases: got %v want %v; error: %v", *released, wantReleased, err)
			}
			if _, err := slots.LoadAcquiredSlotState(options.SharedDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("successful rollback did not remove state: %v", err)
			}
			envFile, _ := slots.EnvFile(options.SharedDir)
			if _, err := os.Stat(envFile); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed acquisition published a runtime contract: %v", err)
			}
		})
	}
}

func TestUnsupportedAssetsAndConflictingSelectorsFailBeforeNetwork(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"unsupported", "missing demanded infra binding", "conflicting selector", "unknown admission kind"} {
		t.Run(scenario, func(t *testing.T) {
			server, acquired, _ := newTestLeaseProxyServer(t, nil)
			defer server.Close()
			options := lifecycleOptions(t, lifecycleCatalog, server.URL, nil)
			expected := "without an implemented handler"
			if scenario == "missing demanded infra binding" {
				options.CatalogPath = writeAcquireTestCatalogFromYAML(t, strings.Replace(lifecycleCatalog, "name: ci01, infrastructure_subscription: dev-infra", "name: ci01", 1))
				expected = "deployment_environment.infrastructure_subscription"
			} else if scenario == "unknown admission kind" {
				options.CatalogPath = writeAcquireTestCatalog(t, slots.RegionModeFixed, "westus3")
				options.DisabledAssetAdmission = []string{"e2e-identities"}
				expected = `--disable-asset-admission: unknown asset kind "e2e-identities"`
			} else if scenario != "unsupported" {
				options.CatalogPath = writeAcquireTestCatalog(t, slots.RegionModeFixed, "westus3")
				options.DeployEnv = "prod"
				expected = "no candidate pool with deploy_env"
			}
			err := Acquire(context.Background(), options)
			if err == nil || !strings.Contains(err.Error(), expected) {
				t.Fatalf("expected selector/handler error %q, got %v", expected, err)
			}
			if len(*acquired) != 0 {
				t.Fatalf("invalid request reached network: %v", *acquired)
			}
		})
	}
}

func TestPoolCommandsPreserveWholeCatalogInventory(t *testing.T) {
	t.Parallel()
	path := writeAcquireTestCatalogFromYAML(t, lifecycleCatalog)
	calls := []string{}
	var assetInventories []slots.AssetInventory
	registry, err := assets.NewRegistry(
		&lifecycleHandler{kind: slots.KindE2EIdentities, calls: &calls},
		&lifecycleHandler{kind: slots.KindInfrastructureIdentities, calls: &calls, assetInventories: &assetInventories},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, validate := range []bool{false, true} {
		err := runPoolAssetsCommand(context.Background(), registry, &assetCommandOptions{
			Environment: "dev", SlotCatalog: path, Pools: []string{"shard0"}, Subscriptions: []string{"dev-e2e"},
			AssetKinds: []string{string(slots.KindInfrastructureIdentities)},
		}, validate)
		if err != nil || len(assetInventories) != 1 || assetInventories[0].Capacity != 5 {
			t.Fatalf("filtered command shrank full demand: %+v, %v", assetInventories, err)
		}
	}
	builtIn, err := newAssetRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if err := runPoolAssetsCommand(context.Background(), builtIn, &assetCommandOptions{Environment: "dev", SlotCatalog: path}, true); err == nil || !strings.Contains(err.Error(), "without an implemented handler") {
		t.Fatalf("unsupported asset management must fail before Azure access: %v", err)
	}
}

func TestPoolHandlersReceiveOnlyDeclaredPoolsAndReferencedInventories(t *testing.T) {
	t.Parallel()
	catalog, err := slots.LoadCatalog(writeAcquireTestCatalogFromYAML(t, lifecycleCatalog))
	if err != nil {
		t.Fatal(err)
	}
	assetInventories, err := catalog.AssetInventories()
	if err != nil {
		t.Fatal(err)
	}
	unrelatedInventory := assetInventories[0]
	unrelatedInventory.AssetPool.Name = "unrelated"
	assetInventories = append(assetInventories, unrelatedInventory)
	calls := []string{}
	var requests []assets.PoolRequest
	registry, err := assets.NewRegistry(
		&lifecycleHandler{kind: slots.KindE2EIdentities, calls: &calls, poolRequests: &requests},
		&lifecycleHandler{kind: slots.KindInfrastructureIdentities, calls: &calls, poolRequests: &requests},
	)
	if err != nil {
		t.Fatal(err)
	}
	pools := append([]slots.Pool{}, catalog.Environments["dev"].Pools...)
	pools = append(pools, catalog.Environments["other"].Pools...)
	e2eOnly := pools[0]
	e2eOnly.Name = "e2e-only"
	e2eOnly.SlotAssets.InfrastructureIdentities = nil
	pools = append(pools, e2eOnly)
	request := assets.PoolRequest{Pools: pools, AssetInventories: assetInventories, IncludeUnmanaged: true}
	for _, operation := range []func(context.Context, assets.PoolRequest, ...assets.Kind) error{registry.ApplyPools, registry.ValidatePools} {
		requests = nil
		if err := operation(t.Context(), request); err != nil {
			t.Fatal(err)
		}
		if len(requests) != 2 {
			t.Fatalf("expected both declared handlers, got %d requests", len(requests))
		}
		if !reflect.DeepEqual(requests[0].Pools, pools) || len(requests[0].AssetInventories) != 0 {
			t.Fatalf("required dedicated handler missed pools or received leased inventories: %+v", requests[0])
		}
		if !reflect.DeepEqual(requests[1].Pools, pools[:2]) || !reflect.DeepEqual(requests[1].AssetInventories, assetInventories[:1]) {
			t.Fatalf("leased handler lost whole-catalog capacity or received unrelated inventory: %+v", requests[1])
		}
		if !requests[0].IncludeUnmanaged || !requests[1].IncludeUnmanaged || len(request.Pools) != 3 || len(request.AssetInventories) != 2 {
			t.Fatal("handler scoping changed request options or the original request")
		}
	}
}

func TestMissingE2EIdentitiesFailsBeforeAcquisition(t *testing.T) {
	t.Parallel()
	catalog := `version: 2
environments:
  dev:
    deployment_environment: {name: ci01, infrastructure_subscription: dev-infra}
    pools:
    - name: shard0
      region: westus3
      slot_count: 1
      subscriptions: {e2e: dev-e2e}
`
	server, acquired, released := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
		"aro-hcp-dev-shard0-slot": {successAcquireReply("aro-hcp-dev-shard0-slot-00")},
	})
	defer server.Close()
	options := lifecycleOptions(t, catalog, server.URL, nil)
	options.ResolveSubscriptions = func(context.Context, string, string, string, string) (slots.ResolvedSubscriptions, error) {
		t.Fatal("invalid catalog reached subscription resolution")
		return slots.ResolvedSubscriptions{}, nil
	}
	if err := Acquire(context.Background(), options); err == nil || !strings.Contains(err.Error(), "must declare slot_assets.e2e_identities") {
		t.Fatalf("expected missing required E2E identities rejection: %v", err)
	}
	if len(*acquired) != 0 || len(*released) != 0 {
		t.Fatalf("invalid catalog reached lease proxy: %v / %v", *acquired, *released)
	}
	if _, err := slots.LoadAcquiredSlotState(options.SharedDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid catalog created acquired state: %v", err)
	}
	path, _ := slots.EnvFile(options.SharedDir)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid catalog published a runtime contract: %v", err)
	}
}

func TestE2EOnlySelectedPoolNeverResolvesInfrastructure(t *testing.T) {
	t.Parallel()
	e2ePool := `    - name: e2e-only
      region: westus3
      slot_count: 1
      subscriptions: {e2e: dev-e2e}
      slot_assets:
        e2e_identities:
          allocation: dedicated
          resource_group_prefix: e2e-only
          resource_group_count: 1
`
	for _, scenario := range []string{"public", "optional binding", "mixed pools"} {
		t.Run(scenario, func(t *testing.T) {
			catalog := "version: 2\nenvironments:\n  dev:\n    deployment_environment: {name: stg}\n    pools:\n" + e2ePool
			deploy := "stg"
			switch scenario {
			case "optional binding":
				catalog = strings.Replace(catalog, "{name: stg}", "{name: stg, infrastructure_subscription: inaccessible-infra}", 1)
			case "mixed pools":
				deploy = "ci01"
				catalog = strings.Replace(lifecycleCatalog, "    pools:\n", "    pools:\n"+e2ePool, 1)
				catalog = strings.Replace(catalog, "subscriptions: {e2e: dev-e2e}\n      slot_assets:\n        e2e_identities:\n          allocation: dedicated\n          resource_group_prefix: identities", "subscriptions: {e2e: other-consumer}\n      slot_assets:\n        e2e_identities:\n          allocation: dedicated\n          resource_group_prefix: identities", 1)
			}
			calls := []string{}
			registry, err := assets.NewRegistry(&lifecycleHandler{kind: slots.KindE2EIdentities, calls: &calls})
			if err != nil {
				t.Fatal(err)
			}
			server, acquired, released := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
				"aro-hcp-dev-e2e-only-slot": {successAcquireReply("aro-hcp-dev-e2e-only-slot-00")},
			})
			defer server.Close()
			options := lifecycleOptions(t, catalog, server.URL, registry)
			options.AllowedSubscriptions = []string{"dev-e2e"}
			resolutions := 0
			options.ResolveSubscriptions = func(_ context.Context, profile, gotDeploy, e2e, infra string) (slots.ResolvedSubscriptions, error) {
				resolutions++
				if gotDeploy != deploy || profile != options.ClusterProfileDir || e2e != "dev-e2e" || infra != "" {
					t.Fatalf("E2E-only pool passed infrastructure demand or wrong target: %q %q %q %q", profile, gotDeploy, e2e, infra)
				}
				return slots.ResolvedSubscriptions{E2E: slots.ResolvedSubscription{Name: e2e, ID: "e2e-id"}}, nil
			}
			if err := Acquire(t.Context(), options); err != nil {
				t.Fatalf("E2E-only acquisition failed: %v", err)
			}
			if resolutions != 1 || len(*acquired) != 1 || len(*released) != 0 {
				t.Fatalf("unexpected resolutions/acquisitions/releases: %d %v %v", resolutions, *acquired, *released)
			}
			state, err := slots.LoadAcquiredSlotState(options.SharedDir)
			if err != nil || state.Slot.Subscriptions.Infrastructure != (slots.ResolvedSubscription{}) {
				t.Fatalf("E2E-only state retained infrastructure subscription: %+v, %v", state, err)
			}
			if err := state.Validate(); err != nil {
				t.Fatalf("E2E-only persisted state is not runtime-ready: %v", err)
			}
			path, _ := slots.EnvFile(options.SharedDir)
			data, err := os.ReadFile(path)
			if err != nil || strings.Contains(string(data), "INFRA_SUBSCRIPTION_ID") || !strings.Contains(string(data), "ARO_HCP_DEPLOY_ENV='"+deploy+"'") || !strings.Contains(string(data), "FAKE_e2e_identities='ready'") {
				t.Fatalf("incorrect E2E-only runtime exports: %s, %v", data, err)
			}
			wantCalls := []string{"resolve:e2e_identities", "admit:e2e_identities", "publish:e2e_identities"}
			if !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("E2E-only lifecycle invoked wrong handlers: %v", calls)
			}
		})
	}
}
