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
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

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
        infrastructure_identities:
          allocation: leased
          asset_pool: bundles
`

type lifecycleHandler struct {
	kind        slots.AssetKind
	calls       *[]string
	fail        string
	before      func(string, assets.LeaseRequest)
	inventories *[]slots.AssetInventory
}

func (h *lifecycleHandler) Kind() assets.Kind { return h.kind }
func (h *lifecycleHandler) Declared(pool slots.Pool) bool {
	for _, requirement := range pool.Requirements() {
		if requirement.Kind == h.kind {
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
		for _, lease := range request.State.Leases.Assets[h.kind] {
			resolved.ResourceGroups = append(resolved.ResourceGroups, lease.ResourceName)
		}
		request.State.Slot.Assets.InfrastructureIdentities = resolved
	}
	return nil
}
func (h *lifecycleHandler) ReleaseLease(ctx context.Context, request assets.LeaseRequest) error {
	*h.calls = append(*h.calls, "release:"+string(h.kind))
	return request.Journal.ReleaseAsset(ctx, h.kind)
}
func (h *lifecycleHandler) ApplyPools(_ context.Context, request assets.PoolRequest) error {
	*h.calls = append(*h.calls, "apply:"+string(h.kind))
	if h.inventories != nil {
		*h.inventories = request.Inventories
	}
	return nil
}
func (h *lifecycleHandler) ValidatePools(ctx context.Context, request assets.PoolRequest) error {
	return h.ApplyPools(ctx, request)
}
func (h *lifecycleHandler) PrepareLease(_ context.Context, request assets.LeaseRequest) error {
	return h.call("prepare", request)
}
func (h *lifecycleHandler) ValidateLease(_ context.Context, request assets.LeaseRequest) error {
	return h.call("validate", request)
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
		Registry:            registry,
		ResolveSubscriptions: func(context.Context, string, string, string, string) (slots.ResolvedSubscriptions, error) {
			return slots.ResolvedSubscriptions{
				E2E:            slots.ResolvedSubscription{Name: "dev-e2e", ID: "e2e-id"},
				Infrastructure: slots.ResolvedSubscription{Name: "dev-infra", ID: "infra-id"},
			}, nil
		},
	}
}

func TestIndependentAssetLifecycleAndRollback(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"success", "resolve", "prepare", "validate", "publish", "second acquire", "unexpected name", "empty secondary", "blank secondary", "padded secondary", "timeout", "primary state write", "secondary state write", "subscription resolution"} {
		t.Run(scenario, func(t *testing.T) {
			calls := []string{}
			infra := &lifecycleHandler{kind: slots.KindInfrastructureIdentities, calls: &calls}
			if scenario == "resolve" || scenario == "prepare" || scenario == "validate" || scenario == "publish" {
				infra.fail = scenario
			}
			registry, err := assets.NewRegistry(
				&lifecycleHandler{kind: slots.KindE2EIdentities, calls: &calls},
				infra,
			)
			if err != nil {
				t.Fatal(err)
			}
			second := successAcquireReply("bundle-01")
			switch scenario {
			case "second acquire":
				second = leaseProxyReply{statusCode: http.StatusForbidden, body: "denied"}
			case "unexpected name":
				second = successAcquireReply("foreign-90")
			case "empty secondary":
				second = successAcquireReply("")
			case "blank secondary":
				second = successAcquireReply(" \t\n")
			case "padded secondary":
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
			writes := 0
			var recorded []int
			options.WriteState = func(dir string, state *slots.AcquiredSlotState) error {
				writes++
				if (scenario == "primary state write" && writes == 1) || (scenario == "secondary state write" && len(state.Leases.Assets[slots.KindInfrastructureIdentities]) == 1) {
					return errors.New("state disk write failed")
				}
				if err := slots.WriteAcquiredSlotState(dir, state); err != nil {
					return err
				}
				recorded = append(recorded, len(state.Leases.Assets[slots.KindInfrastructureIdentities]))
				return nil
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
				if phase == "prepare" && request.State.Slot.Assets.InfrastructureIdentities == nil {
					t.Fatal("prepare ran before resolution")
				}
				env, _ := slots.EnvFile(options.SharedDir)
				if _, err := os.Stat(env); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("runtime contract was visible before all admission/publication passed")
				}
			}
			err = Acquire(context.Background(), options)
			if scenario == "success" {
				if err != nil {
					t.Fatalf("acquire failed: %v", err)
				}
				wantCalls := []string{"resolve:e2e_identities", "resolve:infrastructure_identities", "prepare:e2e_identities", "prepare:infrastructure_identities", "validate:e2e_identities", "validate:infrastructure_identities", "publish:e2e_identities", "publish:infrastructure_identities"}
				if !reflect.DeepEqual(calls, wantCalls) {
					t.Fatalf("incorrect admission order: %v", calls)
				}
				if !reflect.DeepEqual(recorded, []int{0, 0, 1, 2, 2, 2}) {
					t.Fatalf("incremental writes missing: %v", recorded)
				}
				env, _ := slots.EnvFile(options.SharedDir)
				data, err := os.ReadFile(env)
				if err != nil || !strings.Contains(string(data), "ARO_HCP_DEPLOY_ENV='ci01'") || !strings.Contains(string(data), "FAKE_infrastructure_identities='ready'") {
					t.Fatalf("incomplete runtime contract: %s, %v", data, err)
				}
				if len(*released) != 0 || len(*acquired) != 3 {
					t.Fatalf("incorrect acquisitions/releases: %v / %v", *acquired, *released)
				}
				return
			}
			if err == nil {
				t.Fatal("expected fail-closed acquisition")
			}
			wantReleased := []string{"bundle-04", "bundle-01", "aro-hcp-dev-shard0-slot-00"}
			switch scenario {
			case "primary state write", "subscription resolution":
				wantReleased = []string{"aro-hcp-dev-shard0-slot-00"}
			case "second acquire", "empty secondary", "blank secondary", "padded secondary", "timeout", "secondary state write":
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
		})
	}
}

func TestFinalizeV2RejectsMalformedPrimaryBeforeStateWrite(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", " \t\n", " slot-00", "slot-00 ", "\tslot-00\n"} {
		t.Run(name, func(t *testing.T) {
			options := &AcquireOptions{
				completedAcquireOptions: &completedAcquireOptions{
					WriteState: func(string, *slots.AcquiredSlotState) error {
						t.Fatal("invalid primary name reached state persistence")
						return nil
					},
				},
			}
			err := options.finalizeV2Lease(context.Background(), slots.Pool{ResourceType: "slot"}, name)
			if err == nil || !strings.Contains(err.Error(), "resource name") {
				t.Fatalf("expected early primary name rejection, got %v", err)
			}
		})
	}
}

func TestUnsupportedAssetsAndV1SelectorsFailBeforeNetwork(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"unsupported", "missing demanded infra binding", "v1 conflicting selector", "v1 missing selector"} {
		t.Run(scenario, func(t *testing.T) {
			server, acquired, _ := newTestLeaseProxyServer(t, nil)
			defer server.Close()
			options := lifecycleOptions(t, lifecycleCatalog, server.URL, nil)
			expected := "without an implemented handler"
			if scenario == "missing demanded infra binding" {
				options.CatalogPath = writeAcquireTestCatalogFromYAML(t, strings.Replace(lifecycleCatalog, "name: ci01, infrastructure_subscription: dev-infra", "name: ci01", 1))
				expected = "deployment_environment.infrastructure_subscription"
			} else if scenario != "unsupported" {
				options.CatalogPath = writeAcquireTestCatalog(t, slots.RegionModeFixed, "westus3")
				if scenario == "v1 conflicting selector" {
					options.DeployEnv = "prod"
					expected = "does not belong"
				} else {
					expected = "--deploy-env is required"
				}
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
	var inventories []slots.AssetInventory
	registry, err := assets.NewRegistry(
		&lifecycleHandler{kind: slots.KindE2EIdentities, calls: &calls},
		&lifecycleHandler{kind: slots.KindInfrastructureIdentities, calls: &calls, inventories: &inventories},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, validate := range []bool{false, true} {
		err := runPoolAssetsCommand(context.Background(), registry, &assetCommandOptions{
			Environment: "dev", SlotCatalog: path, Pools: []string{"shard0"}, Subscriptions: []string{"dev-e2e"},
			AssetKinds: []string{string(slots.KindInfrastructureIdentities)},
		}, validate)
		if err != nil || len(inventories) != 1 || inventories[0].Capacity != 5 {
			t.Fatalf("filtered command shrank full demand: %+v, %v", inventories, err)
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

func TestV2AbsentAssetsPublishOnlyCoreContract(t *testing.T) {
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
	server, _, released := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
		"aro-hcp-dev-shard0-slot": {successAcquireReply("aro-hcp-dev-shard0-slot-00")},
	})
	defer server.Close()
	options := lifecycleOptions(t, catalog, server.URL, nil)
	if err := Acquire(context.Background(), options); err != nil {
		t.Fatalf("asset-free slot acquisition failed: %v", err)
	}
	path, _ := slots.EnvFile(options.SharedDir)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "LEASED_MSI_CONTAINERS") || strings.Contains(string(data), "INFRA_SUBSCRIPTION_ID") {
		t.Fatalf("absent asset created a runtime contract: %s", data)
	}
	if len(*released) != 0 {
		t.Fatalf("successful acquisition rolled back: %v", *released)
	}
	if err := Acquire(context.Background(), options); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second acquisition should not overwrite existing leases: %v", err)
	}
}

func TestV2E2EOnlySelectedPoolNeverResolvesInfrastructure(t *testing.T) {
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
			wantCalls := []string{"resolve:e2e_identities", "prepare:e2e_identities", "validate:e2e_identities", "publish:e2e_identities"}
			if !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("E2E-only lifecycle invoked wrong handlers: %v", calls)
			}
		})
	}
}
