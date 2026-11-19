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
	"encoding/json"
	"fmt"
	"io/fs"
	"reflect"
	"strings"

	"github.com/Azure/ARO-HCP/test/e2e"
	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/graph"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeployments"
	"k8s.io/utils/ptr"
)

type externalDeployment struct {
	name       string
	parameters map[string]*armdeployments.DeploymentParameter
	template   map[string]any
}

func readExternalRBACTemplate(artifacts fs.FS, name string) (map[string]any, error) {
	data, err := fs.ReadFile(artifacts, "test-artifacts/generated-test-artifacts/external-subscription/"+name+".json")
	if err != nil {
		return nil, fmt.Errorf("read embedded RBAC template %s: %w", name, err)
	}
	var template map[string]any
	if err := json.Unmarshal(data, &template); err != nil {
		return nil, fmt.Errorf("decode embedded RBAC template %s: %w", name, err)
	}
	if len(template) == 0 {
		return nil, fmt.Errorf("embedded RBAC template %s is empty", name)
	}
	return template, nil
}

func (o *ReconcileExternalSubscriptionOptions) sharedDeployments(ctx context.Context) ([]externalDeployment, error) {
	dev := o.config.Clouds.Dev.Defaults.CI.Dev
	var principals []string
	for _, application := range []string{dev.Bot.ApplicationName, dev.MockIdentities.FirstParty.ApplicationName, dev.MockIdentities.ARMHelper.ApplicationName, dev.MockIdentities.MSIMock.ApplicationName} {
		principal, err := o.resolveSharedPrincipal(ctx, application)
		if err != nil {
			return nil, err
		}
		principals = append(principals, principal)
	}
	deployments := []externalDeployment{
		{name: "ci-bot-rbac-subscription", parameters: map[string]*armdeployments.DeploymentParameter{
			"botPrincipalId": {Value: principals[0]}, "isGlobalSubscription": {Value: false},
			"grantAksRbac": {Value: false}, "grantKeyVaultAdmin": {Value: false},
		}},
		{name: "e2e-subscription-rbac-assignment-subscription", parameters: map[string]*armdeployments.DeploymentParameter{
			"firstPartyPrincipalId": {Value: principals[1]}, "armHelperPrincipalId": {Value: principals[2]}, "miMockPrincipalId": {Value: principals[3]},
		}},
	}
	for i := range deployments {
		template, err := readExternalRBACTemplate(e2e.TestArtifactsFS, deployments[i].name)
		if err != nil {
			return nil, err
		}
		deployments[i].template = template
	}
	return deployments, nil
}

func (o *ReconcileExternalSubscriptionOptions) resolveSharedPrincipal(ctx context.Context, displayName string) (string, error) {
	uniqueName := strings.ToLower(strings.ReplaceAll(displayName, " ", "-"))
	application, err := graph.LookupApplication(ctx, o.graph, uniqueName, displayName)
	if err != nil {
		return "", fmt.Errorf("read shared application %q: %w", displayName, err)
	}
	if err := graph.CheckApplicationDisplayName(ctx, o.graph, displayName, ptr.Deref(application.GetId(), "")); err != nil {
		return "", fmt.Errorf("check shared application %q: %w", displayName, err)
	}
	principal, err := graph.LookupServicePrincipal(ctx, o.graph, ptr.Deref(application.GetAppId(), ""))
	if err != nil {
		return "", fmt.Errorf("read shared service principal for %q: %w", displayName, err)
	}
	return ptr.Deref(principal.GetId(), ""), nil
}

func (o *ReconcileExternalSubscriptionOptions) previewDeployment(ctx context.Context, deployment externalDeployment, ready bool) error {
	validation := armdeployments.ValidationLevelProvider
	if !o.Apply {
		validation = armdeployments.ValidationLevelProviderNoRbac
	}
	poller, err := o.deployments.BeginWhatIfAtSubscriptionScope(ctx, "external-"+deployment.name, armdeployments.DeploymentWhatIf{
		Location: to.Ptr("westus3"),
		Properties: &armdeployments.DeploymentWhatIfProperties{
			Mode: to.Ptr(armdeployments.DeploymentModeIncremental), Template: deployment.template,
			Parameters: deployment.parameters, ValidationLevel: &validation,
			WhatIfSettings: &armdeployments.DeploymentWhatIfSettings{ResultFormat: to.Ptr(armdeployments.WhatIfResultFormatFullResourcePayloads)},
		},
	}, nil)
	if err != nil {
		return fmt.Errorf("begin preview %s: %w", deployment.name, err)
	}
	preview, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("preview %s: %w", deployment.name, err)
	}
	if ptr.Deref(preview.Status, "") != "Succeeded" || preview.Error != nil {
		return fmt.Errorf("preview %s did not succeed (status %q): %v", deployment.name, ptr.Deref(preview.Status, ""), preview.Error)
	}
	if preview.Properties == nil || preview.Properties.Changes == nil || len(preview.Properties.PotentialChanges) > 0 {
		return fmt.Errorf("preview %s omitted complete changes; refusing uninspectable readiness or mutation", deployment.name)
	}
	if err := validateExternalRBACChanges(o.SubscriptionID, preview.Properties.Changes, ready); err != nil {
		return fmt.Errorf("%s: %w", deployment.name, err)
	}
	return nil
}

func (o *ReconcileExternalSubscriptionOptions) applyDeployment(ctx context.Context, deployment externalDeployment) error {
	poller, err := o.deployments.BeginCreateOrUpdateAtSubscriptionScope(ctx, "external-"+deployment.name, armdeployments.Deployment{
		Location: to.Ptr("westus3"),
		Properties: &armdeployments.DeploymentProperties{
			Mode: to.Ptr(armdeployments.DeploymentModeIncremental), Template: deployment.template, Parameters: deployment.parameters,
		},
	}, nil)
	if err != nil {
		return fmt.Errorf("begin deployment: %w", err)
	}
	result, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("wait for deployment: %w", err)
	}
	if result.Properties == nil || ptr.Deref(result.Properties.ProvisioningState, "") != "Succeeded" || result.Properties.Error != nil {
		return fmt.Errorf("deployment %s did not succeed: %+v", deployment.name, result.Properties)
	}
	return nil
}

func validateExternalRBACChanges(subscriptionID string, changes []*armdeployments.WhatIfChange, ready bool) error {
	prefix := "/subscriptions/" + strings.ToLower(subscriptionID) + "/providers/microsoft.authorization/"
	for _, change := range changes {
		if change == nil || change.ChangeType == nil {
			return fmt.Errorf("refusing uninspectable RBAC change")
		}
		kind := *change.ChangeType
		if kind == armdeployments.ChangeTypeNoChange || kind == armdeployments.ChangeTypeIgnore {
			continue
		}
		resourceID := ptr.Deref(change.ResourceID, "")
		id := strings.ToLower(resourceID)
		if !strings.HasPrefix(id, prefix+"roleassignments/") && !strings.HasPrefix(id, prefix+"roledefinitions/") {
			return fmt.Errorf("unexpected change outside subscription-only RBAC: %s (%s)", resourceID, kind)
		}
		before, _ := change.Before.(map[string]any)
		after, _ := change.After.(map[string]any)
		switch kind {
		case armdeployments.ChangeTypeCreate:
			if len(after) == 0 {
				return fmt.Errorf("RBAC preview lacks full resource payload for %s", resourceID)
			}
		case armdeployments.ChangeTypeModify:
			if !strings.HasPrefix(id, prefix+"roledefinitions/") || !additiveExternalRoleDefinition(before, after) {
				return fmt.Errorf("refusing RBAC replacement, permission reduction or condition change on %s; use the existing drained maintenance workflow", resourceID)
			}
		default:
			return fmt.Errorf("refusing non-additive or uninspectable RBAC change %s on %s; use the existing drained maintenance workflow", kind, resourceID)
		}
		if ready {
			return fmt.Errorf("RBAC is not ready: %s requires %s", resourceID, kind)
		}
	}
	return nil
}

// Changes to an existing role may only add allowed permissions/scopes. Never remove
// actions or introduce exclusions, even if a newer checkout would otherwise do so.
func additiveExternalRoleDefinition(before, after map[string]any) bool {
	if len(before) == 0 || len(after) == 0 {
		return false
	}
	beforeProperties, ok := before["properties"].(map[string]any)
	if !ok {
		return false
	}
	afterProperties, ok := after["properties"].(map[string]any)
	if !ok {
		return false
	}
	for _, name := range []string{"roleName", "type"} {
		if !reflect.DeepEqual(beforeProperties[name], afterProperties[name]) {
			return false
		}
	}
	if !externalArrayIncludes(afterProperties["assignableScopes"], beforeProperties["assignableScopes"]) {
		return false
	}
	oldPermissions, ok := beforeProperties["permissions"].([]any)
	if !ok || len(oldPermissions) == 0 {
		return false
	}
	newPermissions, ok := afterProperties["permissions"].([]any)
	if !ok {
		return false
	}
	for _, candidate := range newPermissions {
		permission, ok := candidate.(map[string]any)
		if !ok {
			return false
		}
		for _, name := range []string{"notActions", "notDataActions"} {
			exclusions, _ := permission[name].([]any)
			if len(exclusions) > 0 {
				found := false
				for _, previous := range oldPermissions {
					if reflect.DeepEqual(previous, candidate) {
						found = true
					}
				}
				if !found {
					return false
				}
			}
		}
	}
	for _, previous := range oldPermissions {
		old, ok := previous.(map[string]any)
		if !ok {
			return false
		}
		found := false
		for _, candidate := range newPermissions {
			current := candidate.(map[string]any)
			if externalArrayIncludes(current["actions"], old["actions"]) && externalArrayIncludes(current["dataActions"], old["dataActions"]) && reflect.DeepEqual(current["notActions"], old["notActions"]) && reflect.DeepEqual(current["notDataActions"], old["notDataActions"]) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func externalArrayIncludes(superset, subset any) bool {
	old, oldOK := subset.([]any)
	current, newOK := superset.([]any)
	if !oldOK || !newOK {
		return subset == nil && superset == nil
	}
	for _, value := range old {
		found := false
		for _, candidate := range current {
			if reflect.DeepEqual(value, candidate) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
