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
	"reflect"
	"strings"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/util/framework"
)

// PreflightExternalSubscription checks every selected pool before any writes.
// requests must contain all matching DEV/dev-xs pools in the subscription,
// grouped by their logical catalog environment without modifying Pool.DeployEnv.
// No subscription-name lookup or credential acquisition is performed here.
func (h *Handler) PreflightExternalSubscription(ctx context.Context, requests []assets.PoolRequest, subscriptionID string, credential azcore.TokenCredential) error {
	_, _, err := h.preflightExternalSubscription(ctx, requests, subscriptionID, credential)
	return err
}

// ReconcileExternalSubscription repairs only additive identity-pool changes.
// Check mode requires expected containers/identities but ignores unrelated Azure
// inventory. Apply retains the existing deployment-stack delete-on-unmanage
// policy, so a full read-only safety preflight is mandatory before applying.
func (h *Handler) ReconcileExternalSubscription(ctx context.Context, requests []assets.PoolRequest, subscriptionID string, credential azcore.TokenCredential, apply bool) error {
	pools, template, err := h.preflightExternalSubscription(ctx, requests, subscriptionID, credential)
	if err != nil {
		return err
	}
	if apply {
		if err := applyPools(ctx, credential, pools, template, h.newPoolClients); err != nil {
			return err
		}
	}
	var out io.Writer = io.Discard
	for _, request := range requests {
		if request.Out != nil {
			out = request.Out
			break
		}
	}
	return validatePools(ctx, pools, func(ctx context.Context, id string) (subscriptionInventory, error) {
		inventory, err := h.loadExternalInventory(ctx, id, credential)
		if err != nil {
			return subscriptionInventory{}, err
		}
		return expectedExternalInventory(pools, inventory), nil
	}, out)
}

func externalIdentityPools(ctx context.Context, requests []assets.PoolRequest, subscriptionID string) ([]identityPool, error) {
	parsed, err := uuid.Parse(subscriptionID)
	if err != nil || parsed.String() != subscriptionID {
		return nil, fmt.Errorf("external identity pools require a canonical subscription UUID")
	}
	pools := make([]identityPool, 0)
	for _, request := range requests {
		if request.Environment != "dev" && request.Environment != "dev-xs" {
			return nil, fmt.Errorf("external identity pools do not support environment %q", request.Environment)
		}
		for _, pool := range request.Pools {
			resolved, err := resolveIdentityPools(ctx, request.Environment, []slots.Pool{pool}, []string{pool.Subscriptions.E2E}, func(context.Context, string) (string, error) {
				return subscriptionID, nil
			})
			if err != nil {
				return nil, err
			}
			pools = append(pools, resolved...)
		}
	}
	if len(pools) == 0 {
		return nil, fmt.Errorf("no E2E identity pools selected for external subscription %q", subscriptionID)
	}
	return pools, nil
}

func (h *Handler) preflightExternalSubscription(ctx context.Context, requests []assets.PoolRequest, subscriptionID string, credential azcore.TokenCredential) ([]identityPool, map[string]interface{}, error) {
	pools, err := externalIdentityPools(ctx, requests, subscriptionID)
	if err != nil {
		return nil, nil, err
	}
	template, err := readIdentityPoolTemplate()
	if err != nil {
		return nil, nil, err
	}
	clients, err := h.newPoolClients(subscriptionID, credential)
	if err != nil {
		return nil, nil, fmt.Errorf("creating external identity stack client: %w", err)
	}
	expected := map[string]externalIdentityStack{}
	backingGroups := sets.New[string]()
	for _, pool := range pools {
		for _, slot := range pool.Slots {
			name := strings.ToLower(deploymentStackNamePrefix + "-" + slot.ResourceName)
			if _, found := expected[name]; found {
				return nil, nil, fmt.Errorf("duplicate external identity stack %q", name)
			}
			for _, group := range slot.ProvisionedIdentityContainerNames() {
				normalized := strings.ToLower(group)
				if backingGroups.Has(normalized) {
					return nil, nil, fmt.Errorf("identity container %q belongs to multiple selected stacks", group)
				}
				backingGroups.Insert(normalized)
			}
			expected[name] = externalIdentityStack{pool: pool, slot: slot}
		}
	}
	pager := clients.stacks.NewListAtSubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("listing external identity stacks: %w", err)
		}
		for _, stack := range page.Value {
			if stack == nil || stack.Name == nil {
				return nil, nil, fmt.Errorf("deployment stack inventory contains an unnamed stack")
			}
			name := strings.ToLower(*stack.Name)
			if !strings.HasPrefix(name, deploymentStackNamePrefix+"-") {
				continue
			}
			desired, found := expected[name]
			if !found {
				return nil, nil, fmt.Errorf("external identity stack %q was removed or renamed; drain before disruptive maintenance", *stack.Name)
			}
			current, err := clients.stacks.GetAtSubscription(ctx, *stack.Name, nil)
			if err != nil {
				return nil, nil, fmt.Errorf("reading external identity stack %q: %w", *stack.Name, err)
			}
			exported, err := clients.stacks.ExportTemplateAtSubscription(ctx, *stack.Name, nil)
			if err != nil {
				return nil, nil, fmt.Errorf("exporting external identity stack %q template: %w", *stack.Name, err)
			}
			if err := preflightExternalIdentityStack(current.DeploymentStack, exported.Template, desired, template); err != nil {
				return nil, nil, fmt.Errorf("unsafe external identity stack %q: %w; drain before disruptive maintenance", *stack.Name, err)
			}
		}
	}
	return pools, template, nil
}

type externalIdentityStack struct {
	pool identityPool
	slot slots.ExpandedSlot
}

func preflightExternalIdentityStack(current armdeploymentstacks.DeploymentStack, previousTemplate any, desired externalIdentityStack, template map[string]interface{}) error {
	properties := current.Properties
	if properties == nil || properties.Parameters == nil {
		return fmt.Errorf("existing stack has no readable deployment parameters")
	}
	if properties.ProvisioningState != nil {
		switch strings.ToLower(string(*properties.ProvisioningState)) {
		case "succeeded", "failed", "canceled":
		default:
			return fmt.Errorf("another deployment-stack operation is in progress")
		}
	}
	if current.Location == nil || !strings.EqualFold(*current.Location, desired.pool.ProvisioningRegion) {
		return fmt.Errorf("provisioning location changed")
	}
	previous, err := comparableIdentityTemplate(previousTemplate)
	if err != nil {
		return err
	}
	next, err := comparableIdentityTemplate(template)
	if err != nil {
		return err
	}
	if err := preflightAdditiveIdentityTemplate(previous, next); err != nil {
		return fmt.Errorf("deployment template changed: %w", err)
	}
	for name, parameter := range properties.Parameters {
		if parameter == nil {
			return fmt.Errorf("parameter %q is unreadable", name)
		}
		switch name {
		case "poolSize":
			value, err := json.Marshal(parameter.Value)
			if err != nil {
				return fmt.Errorf("reading previous pool size: %w", err)
			}
			var count int
			if err := json.Unmarshal(value, &count); err != nil || count < 0 {
				return fmt.Errorf("previous pool size is not a non-negative integer")
			}
			if desired.slot.EffectiveIdentityContainerProvisioningCount() < count {
				return fmt.Errorf("identity container count decreased from %d to %d", count, desired.slot.EffectiveIdentityContainerProvisioningCount())
			}
		case "resourceGroupBaseName":
			value, ok := parameter.Value.(string)
			if !ok || value != desired.slot.IdentityContainerPrefix {
				return fmt.Errorf("identity container backing prefix changed")
			}
		default:
			definition, ok := template["parameters"].(map[string]interface{})[name].(map[string]interface{})
			if !ok {
				return fmt.Errorf("unknown existing parameter %q", name)
			}
			expected := definition["defaultValue"]
			if name == "location" {
				expected = desired.pool.ProvisioningRegion
			}
			if !reflect.DeepEqual(parameter.Value, expected) {
				return fmt.Errorf("existing parameter %q would change", name)
			}
		}
	}
	for _, required := range []string{"poolSize", "resourceGroupBaseName"} {
		if _, found := properties.Parameters[required]; !found {
			return fmt.Errorf("existing stack is missing parameter %q", required)
		}
	}
	allowed := sets.New[string]()
	for index, group := range desired.slot.ProvisionedIdentityContainerNames() {
		base := "/subscriptions/" + desired.pool.SubscriptionID + "/resourcegroups/" + group
		allowed.Insert(strings.ToLower(base))
		allowed.Insert(strings.ToLower(fmt.Sprintf("%s/providers/Microsoft.Resources/deployments/msi-deployment-%d", base, index)))
		for _, name := range framework.NewDefaultIdentities().ToSlice() {
			allowed.Insert(strings.ToLower(base + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/" + name))
		}
	}
	for _, resource := range properties.Resources {
		if resource == nil || resource.ID == nil || !allowed.Has(strings.ToLower(*resource.ID)) {
			return fmt.Errorf("existing managed backing resource would be removed or replaced: %v", resource)
		}
	}
	return nil
}

func comparableIdentityTemplate(value any) (any, error) {
	if text, ok := value.(string); ok {
		if err := json.Unmarshal([]byte(text), &value); err != nil {
			return nil, fmt.Errorf("decoding existing stack template: %w", err)
		}
	}
	if value == nil {
		return nil, fmt.Errorf("existing stack template is unavailable")
	}
	// Round trip into a private tree; never mutate the caller's embedded template.
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encoding identity template: %w", err)
	}
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return nil, fmt.Errorf("normalizing identity template: %w", err)
	}
	removeIdentityGeneratorMetadata(normalized)
	return normalized, nil
}

func removeIdentityGeneratorMetadata(value any) {
	switch value := value.(type) {
	case map[string]interface{}:
		if metadata, ok := value["metadata"].(map[string]interface{}); ok {
			delete(metadata, "_generator")
			if len(metadata) == 0 {
				delete(value, "metadata")
			}
		}
		for _, child := range value {
			removeIdentityGeneratorMetadata(child)
		}
	case []interface{}:
		for _, child := range value {
			removeIdentityGeneratorMetadata(child)
		}
	}
}

func expectedExternalInventory(pools []identityPool, inventory subscriptionInventory) subscriptionInventory {
	expected := sets.New[string]()
	for _, pool := range pools {
		for _, slot := range pool.Slots {
			for _, group := range slot.ProvisionedIdentityContainerNames() {
				expected.Insert(strings.ToLower(group))
			}
		}
	}
	result := subscriptionInventory{Identities: map[string][]string{}}
	for _, group := range inventory.ResourceGroups {
		if expected.Has(strings.ToLower(group)) {
			result.ResourceGroups = append(result.ResourceGroups, group)
		}
	}
	// Extras, even in a selected container, are not a readiness failure and are
	// never deleted by this operation. The strict regular validator is unchanged.
	names := sets.New[string]()
	for _, name := range framework.NewDefaultIdentities().ToSlice() {
		names.Insert(strings.ToLower(name))
	}
	for group, identities := range inventory.Identities {
		if !expected.Has(strings.ToLower(group)) {
			continue
		}
		for _, name := range identities {
			if names.Has(strings.ToLower(name)) {
				result.Identities[group] = append(result.Identities[group], name)
			}
		}
	}
	return result
}
