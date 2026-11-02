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

package gatherobservability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertsmanagement/armalertsmanagement"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/test/util/junit"
)

// resourceGroupExists reports whether a resource group currently exists. It is a
// single HEAD request (ResourceGroups/CheckExistence) against the resource
// group's own scope, which is the condition the collector actually cares about:
// "is there an environment to observe?".
//
// Do not infer this from the alert APIs. Listing alerts for a resource group
// that does not exist returns an empty 200, and only the metric-rule,
// prometheusRuleGroup and monitoring-workspace calls 404, so error-code sniffing
// on any single collector call is unreliable.
func resourceGroupExists(ctx context.Context, cred azcore.TokenCredential, scope string) (bool, error) {
	id, err := azcorearm.ParseResourceID(scope)
	if err != nil {
		return false, fmt.Errorf("failed to parse resource group ID %s: %w", scope, err)
	}
	client, err := armresources.NewResourceGroupsClient(id.SubscriptionID, cred, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create resource group client: %w", err)
	}
	resp, err := client.CheckExistence(ctx, id.ResourceGroupName, nil)
	if err != nil {
		return false, err
	}
	return resp.Success, nil
}

// unprovisionedJUnit reports one skipped case per workspace, using the same test
// names as the alert collection gate. A skip is deliberate: the environment was
// never provisioned, so neither the presence nor the absence of alert firings
// can be verified. Embedding the reason keeps the skip self-explanatory in the
// Prow UI and in artifacts consumers.
//
// The workspace set is not just the resolved ones. Complete leaves a workspace
// whose configured name is missing or invalid in workspaceErrors instead of
// Workspaces, and that workspace still needs a case: otherwise the svc/hcp/infra
// contract would shrink to two (or one) cases exactly when the environment is
// also absent, and CIHealth would stop seeing the missing test at all.
func (o Options) unprovisionedJUnit(reason error) *junit.TestSuites {
	workspaceTypes := sets.KeySet(o.Workspaces).
		Union(sets.KeySet(o.workspaceErrors)).
		Insert(workspaceInfra)
	testCases := make([]*junit.TestCase, 0, workspaceTypes.Len())
	for _, wsType := range sets.List(workspaceTypes) {
		testCases = append(testCases, &junit.TestCase{
			Name: fmt.Sprintf("[aro-hcp-observability] [%s] alert collection is complete", wsType),
			SkipMessage: &junit.SkipMessage{
				Message: "not evaluated: " + reason.Error(),
			},
		})
	}
	return &junit.TestSuites{Suites: []*junit.TestSuite{{
		Name:       "aro-hcp-tests",
		NumTests:   uint(len(testCases)),
		NumSkipped: uint(len(testCases)),
		TestCases:  testCases,
	}}}
}

// writeUnprovisionedArtifacts keeps the artifact contract of a normal run
// (alerts.json, junit_alerts.xml and the summary page that Spyglass renders) for
// the case where the region resource group does not exist. Metrics panels are
// skipped: they query the same missing infrastructure.
func (o Options) writeUnprovisionedArtifacts(deps gatherDependencies, reason error) error {
	var errs []error
	record := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}

	output := alertsOutput{
		TimeWindow: timeWindow{
			Start: o.TimeWindow.Start.UTC().Format(time.RFC3339),
			End:   o.TimeWindow.End.UTC().Format(time.RFC3339),
		},
		Summary:          alertsSummary{BySeverity: map[armalertsmanagement.Severity]int{}},
		FilterKeys:       []string{},
		FilterOptions:    map[string][]string{},
		CollectionErrors: []string{"alerts: " + reason.Error()},
	}
	content, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		record(fmt.Errorf("failed to marshal alerts.json: %w", err))
	} else if err := deps.writeFile(filepath.Join(o.OutputDir, "alerts.json"), content, 0o644); err != nil {
		record(fmt.Errorf("failed to write alerts.json: %w", err))
	}

	if err := deps.writeJUnit(filepath.Join(o.OutputDir, "junit_alerts.xml"), o.unprovisionedJUnit(reason)); err != nil {
		record(fmt.Errorf("failed to write JUnit output: %w", err))
	}

	// The filename must match the Spyglass HTML lens regex .*-summary.*\.html.
	tabs := []observabilityTab{{Title: "Azure Monitor Alerts", HTML: string(incompleteHTML(nil, reason))}}
	if err := deps.renderPage(filepath.Join(o.OutputDir, "observability-summary.html"), tabs); err != nil {
		record(fmt.Errorf("failed to render observability HTML: %w", err))
	}
	return errors.Join(errs...)
}
