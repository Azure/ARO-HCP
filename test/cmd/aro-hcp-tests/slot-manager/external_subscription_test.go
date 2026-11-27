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
	"slices"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/catalog"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/subscriptions"
)

const (
	externalTestSubscription   = "Hypershift Managed Azure"
	externalTestSubscriptionID = "22222222-2222-2222-2222-222222222222"
	devCIConfigFromPackage     = "../../../../config/config-dev-ci.yaml"
)

func loadExternalTestOptions(t *testing.T, subscription string) *completedReconcileExternalSubscriptionOptions {
	t.Helper()
	validated, err := (&RawReconcileExternalSubscriptionOptions{Subscription: subscription, ConfigFile: devCIConfigFromPackage}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	completed, err := validated.loadSelection()
	if err != nil {
		t.Fatal(err)
	}
	return completed
}

func TestExternalSubscriptionSelectsEveryDevPoolOfTheSubscription(t *testing.T) {
	t.Parallel()
	completed := loadExternalTestOptions(t, externalTestSubscription)
	var environments []string
	for _, request := range completed.requests {
		environments = append(environments, request.Environment)
		if !request.IncludeUnmanaged || len(request.AssetInventories) == 0 {
			t.Fatalf("request %s must include unmanaged pools and the whole-catalog inventory: %+v", request.Environment, request)
		}
	}
	if !slices.Equal(environments, []string{"dev", "dev-xs"}) {
		t.Fatalf("HyperShift backs pools in dev and dev-xs, got %v", environments)
	}
}

func TestExternalSubscriptionRejectsUnsuitableSubscriptions(t *testing.T) {
	t.Parallel()
	slotCatalog, err := catalog.LoadCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	// A managed DEV subscription is prepared by the dev-ci rollout, not by this command.
	var managed string
	for _, pool := range slotCatalog.Environments["dev"].Pools {
		if asset := pool.SlotAssets.E2EIdentities; asset == nil || asset.Provisioning != catalog.IdentityProvisioningUnmanaged {
			managed = pool.Subscriptions.E2E
			break
		}
	}
	for name, test := range map[string]struct{ subscription, want string }{
		"unknown":  {"no such subscription", "no dev or dev-xs pool uses subscription"},
		"managed":  {managed, "not an unmanaged E2E identity pool"},
		"id given": {externalTestSubscriptionID, "no dev or dev-xs pool uses subscription"},
	} {
		if _, err := selectExternalPools(slotCatalog, test.subscription); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%s: want %q, got %v", name, test.want, err)
		}
	}
}

func TestExternalSubscriptionValidateRequiresNameAndConfig(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string]*RawReconcileExternalSubscriptionOptions{
		"no subscription": {ConfigFile: "config.yaml"},
		"no config":       {Subscription: externalTestSubscription},
	} {
		if _, err := raw.Validate(); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
}

type externalStepRecorder struct {
	calls  []string
	failAt string
	deploy []subscriptions.Deployment
}

func (r *externalStepRecorder) step(name string) error {
	r.calls = append(r.calls, name)
	if r.failAt == name {
		return errors.New(name + " failed")
	}
	return nil
}

func (r *externalStepRecorder) steps() externalSubscriptionSteps {
	return externalSubscriptionSteps{
		resolveSubscriptionID: func(context.Context, string) (string, error) {
			return externalTestSubscriptionID, r.step("resolve")
		},
		registerProviders: func(_ context.Context, ids, namespaces []string) error {
			if !slices.Equal(ids, []string{externalTestSubscriptionID}) || !slices.Contains(namespaces, "Microsoft.Compute") {
				return errors.New("unexpected provider registration scope")
			}
			return r.step("providers")
		},
		resolvePrincipalID: func(_ context.Context, name string) (string, error) {
			return "principal-of-" + name, r.step("principal")
		},
		deploy: func(_ context.Context, ids []string, deployments ...subscriptions.Deployment) error {
			r.deploy = deployments
			return r.step("rbac")
		},
		applyContainers: func(_ context.Context, request assets.PoolRequest) error {
			return r.step("apply-containers-" + request.Environment)
		},
		validateContainers: func(_ context.Context, request assets.PoolRequest) error {
			return r.step("validate-containers-" + request.Environment)
		},
		reconcileMockRoles: func(_ context.Context, request assets.PoolRequest, apply bool) error {
			if apply {
				return r.step("grant-mock-roles-" + request.Environment)
			}
			return r.step("check-mock-roles-" + request.Environment)
		},
	}
}

func TestExternalSubscriptionRunOrdersStepsAndGrantsBotAKSRBAC(t *testing.T) {
	t.Parallel()
	completed := loadExternalTestOptions(t, externalTestSubscription)
	recorder := &externalStepRecorder{}
	completed.steps = recorder.steps()
	if err := (&ReconcileExternalSubscriptionOptions{completedReconcileExternalSubscriptionOptions: completed}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"resolve", "providers", "principal", "principal", "principal", "principal", "rbac",
		"apply-containers-dev", "apply-containers-dev-xs", "grant-mock-roles-dev", "grant-mock-roles-dev-xs",
		"validate-containers-dev", "validate-containers-dev-xs", "check-mock-roles-dev", "check-mock-roles-dev-xs"}
	if !slices.Equal(recorder.calls, want) {
		t.Fatalf("steps ran as\n%v\nwant\n%v", recorder.calls, want)
	}
	// Regression: the DEV pipeline grants the bot AKS RBAC; omitting it silently broke external subscriptions.
	if len(recorder.deploy) != 2 || recorder.deploy[0].Parameters["grantAksRbac"] != true || recorder.deploy[0].Parameters["grantKeyVaultAdmin"] != false {
		t.Fatalf("unexpected RBAC deployments: %+v", recorder.deploy)
	}
}

func TestExternalSubscriptionRunStopsAtFirstFailure(t *testing.T) {
	t.Parallel()
	for _, failAt := range []string{"providers", "principal", "rbac", "apply-containers-dev", "grant-mock-roles-dev", "validate-containers-dev", "check-mock-roles-dev"} {
		completed := loadExternalTestOptions(t, externalTestSubscription)
		recorder := &externalStepRecorder{failAt: failAt}
		completed.steps = recorder.steps()
		err := (&ReconcileExternalSubscriptionOptions{completedReconcileExternalSubscriptionOptions: completed}).Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), failAt+" failed") {
			t.Fatalf("%s: expected the failure to be reported, got %v", failAt, err)
		}
		if recorder.calls[len(recorder.calls)-1] != failAt {
			t.Fatalf("%s: later steps ran after the failure: %v", failAt, recorder.calls)
		}
	}
}
