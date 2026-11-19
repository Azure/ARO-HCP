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

package e2eidentities

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

const externalTestSubscription = "00000000-0000-0000-0000-000000000001"

type externalStackTransport struct {
	stacks   []armdeploymentstacks.DeploymentStack
	template any
	writes   []armdeploymentstacks.DeploymentStack
}

func (transport *externalStackTransport) Do(request *http.Request) (*http.Response, error) {
	var payload any
	switch {
	case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/deploymentStacks"):
		payload = map[string]any{"value": transport.stacks}
	case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/exportTemplate"):
		payload = map[string]any{"template": transport.template}
	case request.Method == http.MethodGet:
		for _, stack := range transport.stacks {
			if strings.HasSuffix(request.URL.Path, "/"+*stack.Name) {
				payload = stack
				break
			}
		}
		if payload == nil {
			return nil, fmt.Errorf("unexpected stack GET %s", request.URL.Path)
		}
	case request.Method == http.MethodPut && strings.Contains(request.URL.Path, "/deploymentStacks/"):
		var stack armdeploymentstacks.DeploymentStack
		if err := json.NewDecoder(request.Body).Decode(&stack); err != nil {
			return nil, err
		}
		transport.writes = append(transport.writes, stack)
		stack.ID = to.Ptr(request.URL.Path)
		stack.Properties.ProvisioningState = to.Ptr(armdeploymentstacks.DeploymentStackProvisioningStateSucceeded)
		payload = stack
	default:
		// In particular no identity deletion, grant removal, or resource-group
		// mutation is a legitimate direct operation of subscription maintenance.
		return nil, fmt.Errorf("unexpected modifying SDK request %s %s", request.Method, request.URL.Path)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(data))),
		Request:    request,
	}, nil
}

func externalTestRequest() assets.PoolRequest {
	return assets.PoolRequest{Environment: "dev", IncludeUnmanaged: true, Pools: []slots.Pool{{
		Name: "external-dev", DeployEnv: "dev", ResourceType: "external-dev-slot", Region: "westus3", SlotCount: 1,
		Subscriptions: slots.PoolSubscriptions{E2E: "external-subscription"},
		SlotAssets: slots.SlotAssets{E2EIdentities: &slots.E2EIdentitiesAsset{
			Allocation: slots.Allocation("dedicated"), Provisioning: slots.IdentityProvisioningUnmanaged,
			ResourceGroupPrefix: "external-identities", ResourceGroupCount: 2,
		}},
	}}}
}

func externalTestHandler(t *testing.T, requests ...assets.PoolRequest) (*Handler, *externalStackTransport, []identityPool) {
	t.Helper()
	pools, err := externalIdentityPools(context.Background(), requests, externalTestSubscription)
	if err != nil {
		t.Fatal(err)
	}
	template, err := readIdentityPoolTemplate()
	if err != nil {
		t.Fatal(err)
	}
	transport := &externalStackTransport{template: template}
	handler := NewHandler(WithPoolClients(func(subscriptionID string, credential azcore.TokenCredential) (poolClients, error) {
		if subscriptionID != externalTestSubscription {
			t.Fatalf("subscription escaped pinned target: %s", subscriptionID)
		}
		client, err := armdeploymentstacks.NewClient(subscriptionID, credential, &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transport}})
		return poolClients{stacks: client}, err
	}))
	// No Azure subscription-name resolution may happen in this API.
	handler.poolDependencies = func() (azcore.TokenCredential, subscriptionIDResolverFunc, error) {
		t.Fatal("external maintenance acquired credentials or resolved a subscription name")
		return nil, nil, nil
	}
	handler.loadExternalInventory = func(context.Context, string, azcore.TokenCredential) (subscriptionInventory, error) {
		inventory := subscriptionInventory{Identities: map[string][]string{}}
		for _, pool := range pools {
			for _, slot := range pool.Slots {
				for _, group := range slot.ProvisionedIdentityContainerNames() {
					inventory.ResourceGroups = append(inventory.ResourceGroups, group)
					inventory.Identities[group] = framework.NewDefaultIdentities().ToSlice()
				}
			}
		}
		return inventory, nil
	}
	return handler, transport, pools
}

func externalExistingStack(pool identityPool, slot slots.ExpandedSlot, count int) armdeploymentstacks.DeploymentStack {
	return armdeploymentstacks.DeploymentStack{
		Name: to.Ptr(deploymentStackNamePrefix + "-" + slot.ResourceName), Location: to.Ptr(pool.ProvisioningRegion),
		Properties: &armdeploymentstacks.DeploymentStackProperties{
			Parameters: map[string]*armdeploymentstacks.DeploymentParameter{
				"poolSize": {Value: count}, "resourceGroupBaseName": {Value: slot.IdentityContainerPrefix},
			},
		},
	}
}

func TestExternalSubscriptionRejectsDestructiveChangesBeforeAnyWrite(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*externalStackTransport, []identityPool)
		want   string
	}{
		{"count shrink", func(transport *externalStackTransport, _ []identityPool) {
			transport.stacks[1].Properties.Parameters["poolSize"].Value = 3
		}, "count decreased"},
		{"prefix rename", func(transport *externalStackTransport, _ []identityPool) {
			transport.stacks[1].Properties.Parameters["resourceGroupBaseName"].Value = "old-prefix"
		}, "backing prefix changed"},
		{"removed slot", func(transport *externalStackTransport, _ []identityPool) {
			transport.stacks[1].Name = to.Ptr(deploymentStackNamePrefix + "-retired-slot")
		}, "removed or renamed"},
		{"region replacement", func(transport *externalStackTransport, _ []identityPool) {
			transport.stacks[1].Location = to.Ptr("eastus")
		}, "location changed"},
		{"unmanaged old identity", func(transport *externalStackTransport, pools []identityPool) {
			group := pools[1].Slots[0].ProvisionedIdentityContainerNames()[0]
			transport.stacks[1].Properties.Resources = []*armdeploymentstacks.ManagedResourceReference{{ID: to.Ptr("/subscriptions/" + externalTestSubscription + "/resourceGroups/" + group + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/old-identity")}}
		}, "removed or replaced"},
		{"nested template change", func(transport *externalStackTransport, _ []identityPool) {
			template := transport.template.(map[string]interface{})
			resources := template["resources"].([]interface{})
			nested := resources[1].(map[string]interface{})["properties"].(map[string]interface{})["template"].(map[string]interface{})
			nested["resources"].(map[string]interface{})["controlPlaneMi"].(map[string]interface{})["location"] = "eastus"
		}, "template changed"},
		{"identity rename", func(transport *externalStackTransport, _ []identityPool) {
			template := transport.template.(map[string]interface{})
			template["variables"].(map[string]interface{})["identities"].(map[string]interface{})["kmsMiName"] = "old-kms-name"
		}, "template changed"},
		{"identity removal", func(transport *externalStackTransport, _ []identityPool) {
			template := transport.template.(map[string]interface{})
			resources := template["resources"].([]interface{})
			nested := resources[1].(map[string]interface{})["properties"].(map[string]interface{})["template"].(map[string]interface{})
			nested["resources"].(map[string]interface{})["retiredIdentity"] = map[string]interface{}{
				"apiVersion": "2023-01-31", "type": "Microsoft.ManagedIdentity/userAssignedIdentities",
				"location": "[resourceGroup().location]", "name": "retired-identity",
			}
		}, "template changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := externalTestRequest()
			second := request.Pools[0]
			second.Name, second.ResourceType = "external-dev-xs", "external-dev-xs-slot"
			second.DeployEnv = "ci02"
			second.SlotAssets.E2EIdentities = &slots.E2EIdentitiesAsset{Allocation: slots.Allocation("dedicated"), ResourceGroupPrefix: "external-xs-identities", ResourceGroupCount: 2}
			request.Pools[0].DeployEnv = "ci01"
			requests := []assets.PoolRequest{request, {Environment: "dev-xs", Pools: []slots.Pool{second}}}
			handler, transport, pools := externalTestHandler(t, requests...)
			for _, pool := range pools {
				transport.stacks = append(transport.stacks, externalExistingStack(pool, pool.Slots[0], 2))
			}
			test.change(transport, pools)
			err := handler.ReconcileExternalSubscription(context.Background(), requests, externalTestSubscription, admissionCredential{}, true)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q before writes, got %v", test.want, err)
			}
			if len(transport.writes) != 0 {
				t.Fatalf("unsafe second pool allowed %d modifying operations", len(transport.writes))
			}
		})
	}
}

func TestExternalSubscriptionAllowsFreshAndAdditivePools(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing=%t", existing), func(t *testing.T) {
			request := externalTestRequest()
			handler, transport, pools := externalTestHandler(t, request)
			if existing {
				transport.stacks = []armdeploymentstacks.DeploymentStack{externalExistingStack(pools[0], pools[0].Slots[0], 1)}
				group := pools[0].Slots[0].ProvisionedIdentityContainerNames()[0]
				transport.stacks[0].Properties.Resources = []*armdeploymentstacks.ManagedResourceReference{{ID: to.Ptr("/subscriptions/" + externalTestSubscription + "/resourceGroups/" + group + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/control-plane")}}
			}
			if err := handler.PreflightExternalSubscription(context.Background(), []assets.PoolRequest{request}, externalTestSubscription, admissionCredential{}); err != nil {
				t.Fatal(err)
			}
			if len(transport.writes) != 0 {
				t.Fatal("preflight modified Azure")
			}
			if err := handler.ReconcileExternalSubscription(context.Background(), []assets.PoolRequest{request}, externalTestSubscription, admissionCredential{}, true); err != nil {
				t.Fatal(err)
			}
			if len(transport.writes) != 1 {
				t.Fatalf("expected one additive stack update, got %d", len(transport.writes))
			}
			stack := transport.writes[0]
			if stack.Properties.ActionOnUnmanage.Resources == nil || *stack.Properties.ActionOnUnmanage.Resources != armdeploymentstacks.DeploymentStacksDeleteDetachEnum("delete") {
				t.Fatal("external maintenance silently changed ordinary stack deletion semantics")
			}
			if stack.Properties.Parameters["poolSize"].Value != float64(2) {
				t.Fatalf("unexpected additive size: %v", stack.Properties.Parameters["poolSize"].Value)
			}
		})
	}
}

func TestExternalSubscriptionCheckScopesInventoryAndRequiresReadiness(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing=%t", missing), func(t *testing.T) {
			request := externalTestRequest()
			handler, transport, _ := externalTestHandler(t, request)
			load := handler.loadExternalInventory
			handler.loadExternalInventory = func(ctx context.Context, id string, credential azcore.TokenCredential) (subscriptionInventory, error) {
				inventory, err := load(ctx, id, credential)
				inventory.ResourceGroups = append(inventory.ResourceGroups, "external-identities-unrelated")
				inventory.Identities["external-identities-unrelated"] = []string{"unrelated"}
				for group, names := range inventory.Identities {
					if missing && len(names) > 1 {
						inventory.Identities[group] = names[1:]
					}
					inventory.Identities[group] = append(inventory.Identities[group], "preserve-extra-identity")
				}
				return inventory, err
			}
			err := handler.ReconcileExternalSubscription(context.Background(), []assets.PoolRequest{request}, externalTestSubscription, admissionCredential{}, false)
			if (err != nil) != missing {
				t.Fatalf("readiness error = %v, missing = %t", err, missing)
			}
			if len(transport.writes) != 0 {
				t.Fatal("check mode modified Azure")
			}
		})
	}
}

func TestExternalSubscriptionRejectsInvalidTargetsBeforeAzure(t *testing.T) {
	for _, test := range []struct{ name, environment, subscription string }{
		{"bad uuid", "dev", "display-name"},
		{"other environment", "int", externalTestSubscription},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := externalTestRequest()
			request.Environment = test.environment
			handler := NewHandler(WithPoolClients(func(string, azcore.TokenCredential) (poolClients, error) {
				t.Fatal("invalid target reached Azure")
				return poolClients{}, nil
			}))
			if err := handler.PreflightExternalSubscription(context.Background(), []assets.PoolRequest{request}, test.subscription, admissionCredential{}); err == nil {
				t.Fatal("invalid target accepted")
			}
		})
	}
}

func TestExternalSubscriptionAddsRequiredIdentityWithoutChangingExistingIdentities(t *testing.T) {
	request := externalTestRequest()
	handler, transport, pools := externalTestHandler(t, request)
	transport.stacks = []armdeploymentstacks.DeploymentStack{externalExistingStack(pools[0], pools[0].Slots[0], 2)}
	previous := transport.template.(map[string]interface{})
	resources := previous["resources"].([]interface{})
	nested := resources[1].(map[string]interface{})["properties"].(map[string]interface{})["template"].(map[string]interface{})
	previousResources := nested["resources"].(map[string]interface{})
	// Simulate an existing deployed template from before kms became required.
	// All other identities and their principal-scoped grants are active.
	delete(previous["variables"].(map[string]interface{})["identities"].(map[string]interface{}), "kmsMiName")
	delete(nested["definitions"].(map[string]interface{})["ManagedIdentities"].(map[string]interface{})["properties"].(map[string]interface{}), "kmsMiName")
	delete(nested["outputs"].(map[string]interface{})["msiIdentities"].(map[string]interface{})["value"].(map[string]interface{}), "kmsMiName")
	delete(previousResources, "kmsMi")
	group := pools[0].Slots[0].ProvisionedIdentityContainerNames()[0]
	identityID := "/subscriptions/" + externalTestSubscription + "/resourceGroups/" + group + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/control-plane"
	transport.stacks[0].Properties.Resources = []*armdeploymentstacks.ManagedResourceReference{{ID: &identityID}}
	if err := handler.ReconcileExternalSubscription(context.Background(), []assets.PoolRequest{request}, externalTestSubscription, admissionCredential{}, true); err != nil {
		t.Fatalf("adding a newly required identity failed: %v", err)
	}
	if len(transport.writes) != 1 {
		t.Fatalf("expected one non-destructive stack update, got %d", len(transport.writes))
	}
	next := transport.writes[0].Properties.Template.(map[string]interface{})
	nextResources := next["resources"].([]interface{})[1].(map[string]interface{})["properties"].(map[string]interface{})["template"].(map[string]interface{})["resources"].(map[string]interface{})
	if _, found := nextResources["kmsMi"]; !found {
		t.Fatal("new required identity was not deployed")
	}
	// UAMI principal IDs and their grants survive incremental PUT only when
	// existing physical names, locations, and resource properties stay intact.
	// The fake SDK transport rejects every direct identity/grant/delete request.
	for name, resource := range previousResources {
		if !reflect.DeepEqual(resource, nextResources[name]) {
			t.Fatalf("adding kms changed existing identity %s: old=%v new=%v", name, resource, nextResources[name])
		}
	}
}
