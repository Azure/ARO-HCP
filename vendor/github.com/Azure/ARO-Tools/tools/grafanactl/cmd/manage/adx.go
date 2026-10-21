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

package manage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-Tools/tools/grafanactl/internal/azure"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dashboard/armdashboard/v2"
)

const (
	adxPurposeTagKey      = "aroHCPPurpose"
	adxPurposeTagValue    = "logs"
	adxManagedByTagKey    = "aroHCPManagedBy"
	adxManagedByTagValue  = "grafanactl"
	adxGeographyTagKey    = "aroHCPGeoShortId"
	adxIntegrationPrefix  = "adx-"
	adxIntegrationHashLen = 16
)

type desiredADXIntegration struct {
	Name                 string
	DataSourceResourceID string
	Scenario             string
	TargetResourceID     string
}

type adxReconcileOperationType string

const (
	adxReconcileCreate   adxReconcileOperationType = "create"
	adxReconcileUpdate   adxReconcileOperationType = "update"
	adxReconcileDelete   adxReconcileOperationType = "delete"
	adxReconcileRecreate adxReconcileOperationType = "recreate"
)

type adxReconcileOperation struct {
	Type    adxReconcileOperationType
	Name    string
	Desired *desiredADXIntegration
}

func normalizeResourceID(resourceID string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(resourceID), "/"))
}

func integrationFabricName(resourceID string) string {
	sum := sha256.Sum256([]byte(normalizeResourceID(resourceID)))
	return adxIntegrationPrefix + hex.EncodeToString(sum[:adxIntegrationHashLen/2])
}

func parseGeographyAllowlist(geographies string) map[string]struct{} {
	allowlist := map[string]struct{}{}
	for geography := range strings.SplitSeq(geographies, ",") {
		geography = strings.ToLower(strings.TrimSpace(geography))
		if geography != "" {
			allowlist[geography] = struct{}{}
		}
	}
	return allowlist
}

func selectKustoClustersByGeography(clusters []azure.KustoCluster, geographies string) ([]azure.KustoCluster, error) {
	allowlist := parseGeographyAllowlist(geographies)
	if len(allowlist) == 0 {
		return nil, fmt.Errorf("geography allowlist cannot be empty")
	}

	matches := make(map[string][]azure.KustoCluster, len(allowlist))
	for _, cluster := range clusters {
		geography := strings.ToLower(strings.TrimSpace(cluster.Geography))
		if geography == "" {
			return nil, fmt.Errorf("kusto cluster %q is missing the %s tag required for authoritative geography selection", cluster.ResourceID, adxGeographyTagKey)
		}
		if _, requested := allowlist[geography]; requested {
			matches[geography] = append(matches[geography], cluster)
		}
	}

	requestedGeographies := make([]string, 0, len(allowlist))
	for geography := range allowlist {
		requestedGeographies = append(requestedGeographies, geography)
	}
	slices.Sort(requestedGeographies)

	selected := make([]azure.KustoCluster, 0, len(requestedGeographies))
	for _, geography := range requestedGeographies {
		clustersForGeography := matches[geography]
		switch len(clustersForGeography) {
		case 0:
			return nil, fmt.Errorf("requested geography %q has no matching managed Kusto cluster; no integration fabrics were listed or modified", geography)
		case 1:
		default:
			return nil, fmt.Errorf("requested geography %q has %d matching managed Kusto clusters; expected exactly one and no integration fabrics were listed or modified", geography, len(clustersForGeography))
		}

		cluster := clustersForGeography[0]
		if !strings.EqualFold(strings.TrimSpace(cluster.ProvisioningState), "Succeeded") {
			return nil, fmt.Errorf("managed Kusto cluster %q for geography %q has provisioning state %q, not Succeeded; no integration fabrics were listed or modified", cluster.ResourceID, geography, cluster.ProvisioningState)
		}
		resourceID := normalizeResourceID(cluster.ResourceID)
		if resourceID == "" {
			return nil, fmt.Errorf("managed Kusto cluster for geography %q has an empty resource ID; no integration fabrics were listed or modified", geography)
		}
		selected = append(selected, azure.KustoCluster{
			ResourceID:        resourceID,
			Geography:         geography,
			ProvisioningState: cluster.ProvisioningState,
		})
	}
	return selected, nil
}

func desiredADXIntegrations(clusters []azure.KustoCluster, scenario, targetResourceID string) []desiredADXIntegration {
	scenario = strings.TrimSpace(scenario)
	targetResourceID = strings.TrimSpace(targetResourceID)
	desired := make([]desiredADXIntegration, 0, len(clusters))
	for _, cluster := range clusters {
		resourceID := normalizeResourceID(cluster.ResourceID)
		desired = append(desired, desiredADXIntegration{
			Name:                 integrationFabricName(resourceID),
			DataSourceResourceID: resourceID,
			Scenario:             scenario,
			TargetResourceID:     targetResourceID,
		})
	}
	slices.SortFunc(desired, func(a, b desiredADXIntegration) int {
		return strings.Compare(a.Name, b.Name)
	})
	return desired
}

func planADXReconciliation(desired []desiredADXIntegration, existing []armdashboard.IntegrationFabric) ([]adxReconcileOperation, error) {
	existingByName := make(map[string]armdashboard.IntegrationFabric, len(existing))
	for _, fabric := range existing {
		if fabric.Name == nil || *fabric.Name == "" {
			continue
		}
		existingByName[strings.ToLower(*fabric.Name)] = fabric
	}

	desiredNames := make(map[string]struct{}, len(desired))
	var operations []adxReconcileOperation
	for i := range desired {
		item := &desired[i]
		name := strings.ToLower(item.Name)
		desiredNames[name] = struct{}{}

		fabric, found := existingByName[name]
		if !found {
			operations = append(operations, adxReconcileOperation{Type: adxReconcileCreate, Name: item.Name, Desired: item})
			continue
		}
		if !isOwnedIntegrationFabric(fabric) {
			return nil, fmt.Errorf("integration fabric %q already exists but is not owned by grafanactl", item.Name)
		}
		if integrationFabricNeedsRecreate(fabric, *item) {
			operations = append(operations, adxReconcileOperation{Type: adxReconcileRecreate, Name: item.Name, Desired: item})
			continue
		}
		if integrationFabricNeedsScenarioUpdate(fabric, *item) {
			operations = append(operations, adxReconcileOperation{Type: adxReconcileUpdate, Name: item.Name, Desired: item})
		}
	}

	var staleNames []string
	for _, fabric := range existing {
		if fabric.Name == nil || !isOwnedIntegrationFabric(fabric) {
			continue
		}
		name := strings.ToLower(*fabric.Name)
		if _, found := desiredNames[name]; !found {
			staleNames = append(staleNames, *fabric.Name)
		}
	}
	slices.Sort(staleNames)
	for _, name := range staleNames {
		operations = append(operations, adxReconcileOperation{Type: adxReconcileDelete, Name: name})
	}

	return operations, nil
}

func isOwnedIntegrationFabric(fabric armdashboard.IntegrationFabric) bool {
	if hasTag(fabric.Tags, adxPurposeTagKey) || hasTag(fabric.Tags, adxManagedByTagKey) {
		return tagEquals(fabric.Tags, adxPurposeTagKey, adxPurposeTagValue) &&
			tagEquals(fabric.Tags, adxManagedByTagKey, adxManagedByTagValue)
	}
	if fabric.Name == nil ||
		fabric.Properties == nil ||
		fabric.Properties.DataSourceResourceID == nil ||
		normalizeResourceID(*fabric.Properties.DataSourceResourceID) == "" {
		return false
	}
	return *fabric.Name == integrationFabricName(*fabric.Properties.DataSourceResourceID)
}

func hasTag(tags map[string]*string, key string) bool {
	for tagKey := range tags {
		if strings.EqualFold(tagKey, key) {
			return true
		}
	}
	return false
}

func tagEquals(tags map[string]*string, key, value string) bool {
	for tagKey, tagValue := range tags {
		if strings.EqualFold(tagKey, key) && tagValue != nil && strings.EqualFold(*tagValue, value) {
			return true
		}
	}
	return false
}

func integrationFabricNeedsRecreate(fabric armdashboard.IntegrationFabric, desired desiredADXIntegration) bool {
	if fabric.Properties == nil || fabric.Properties.DataSourceResourceID == nil {
		return true
	}
	if normalizeResourceID(*fabric.Properties.DataSourceResourceID) != normalizeResourceID(desired.DataSourceResourceID) {
		return true
	}
	if strings.TrimSpace(desired.TargetResourceID) == "" {
		return false
	}
	return normalizeResourceID(valueOrEmpty(fabric.Properties.TargetResourceID)) != normalizeResourceID(desired.TargetResourceID)
}

func integrationFabricNeedsScenarioUpdate(fabric armdashboard.IntegrationFabric, desired desiredADXIntegration) bool {
	if strings.TrimSpace(desired.Scenario) == "" {
		return false
	}
	existing := make([]string, 0, len(fabric.Properties.Scenarios))
	for _, scenario := range fabric.Properties.Scenarios {
		if scenario != nil {
			existing = append(existing, *scenario)
		}
	}
	expected := scenarioValues(desired.Scenario)
	if len(existing) != len(expected) {
		return true
	}
	for i := range existing {
		if !strings.EqualFold(existing[i], expected[i]) {
			return true
		}
	}
	return false
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func scenarioValues(scenario string) []string {
	scenario = strings.TrimSpace(scenario)
	if scenario == "" {
		return nil
	}
	return []string{scenario}
}

func integrationFabricResource(location string, desired desiredADXIntegration) armdashboard.IntegrationFabric {
	properties := &armdashboard.IntegrationFabricProperties{
		DataSourceResourceID: to.Ptr(desired.DataSourceResourceID),
	}
	if scenarios := scenarioValues(desired.Scenario); len(scenarios) > 0 {
		properties.Scenarios = []*string{to.Ptr(scenarios[0])}
	}
	if desired.TargetResourceID != "" {
		properties.TargetResourceID = to.Ptr(desired.TargetResourceID)
	}

	return armdashboard.IntegrationFabric{
		Location: to.Ptr(location),
		Tags: map[string]*string{
			adxPurposeTagKey:   to.Ptr(adxPurposeTagValue),
			adxManagedByTagKey: to.Ptr(adxManagedByTagValue),
		},
		Properties: properties,
	}
}

func (o *CompletedReconcileOptions) reconcileADXIntegrations(ctx context.Context, location string, logger logr.Logger) error {
	if !o.ADXIntegrationsEnabled {
		return nil
	}

	clusters, err := o.KustoDiscoveryClient.DiscoverKustoClusters(ctx, o.SubscriptionID, o.ADXEnvironment)
	if err != nil {
		return fmt.Errorf("failed to discover ADX Kusto clusters via Resource Graph: %w", err)
	}
	selectedClusters, err := selectKustoClustersByGeography(clusters, o.ADXGeographies)
	if err != nil {
		return err
	}

	existing, err := o.IntegrationFabricsClient.List(ctx, o.ResourceGroup, o.GrafanaName)
	if err != nil {
		var responseError *azcore.ResponseError
		if o.DryRun && errors.As(err, &responseError) && responseError.StatusCode == 404 {
			logger.Info("Grafana instance does not exist yet; planning ADX integration fabrics against an empty existing set")
			existing = nil
		} else {
			return fmt.Errorf("failed to list Grafana integration fabrics: %w", err)
		}
	}

	desired := desiredADXIntegrations(selectedClusters, o.ADXScenario, o.ADXTargetResourceID)
	operations, err := planADXReconciliation(desired, existing)
	if err != nil {
		return fmt.Errorf("failed to plan ADX integration fabric reconciliation: %w", err)
	}

	logger.Info("reconciling ADX integration fabrics",
		"discovered-clusters", len(clusters),
		"selected-clusters", len(selectedClusters),
		"operations", len(operations),
	)
	return o.applyADXReconciliationOperations(ctx, location, operations, logger)
}

func (o *CompletedReconcileOptions) applyADXReconciliationOperations(ctx context.Context, location string, operations []adxReconcileOperation, logger logr.Logger) error {
	for _, operation := range operations {
		logger.Info("ADX integration fabric operation", "operation", operation.Type, "name", operation.Name, "dry-run", o.DryRun)
		if o.DryRun {
			continue
		}
		switch operation.Type {
		case adxReconcileCreate:
			if _, err := o.IntegrationFabricsClient.Create(ctx, o.ResourceGroup, o.GrafanaName, operation.Name, integrationFabricResource(location, *operation.Desired)); err != nil {
				return err
			}
		case adxReconcileUpdate:
			parameters := armdashboard.IntegrationFabricUpdateParameters{
				Properties: &armdashboard.IntegrationFabricPropertiesUpdateParameters{
					Scenarios: []*string{to.Ptr(strings.TrimSpace(operation.Desired.Scenario))},
				},
			}
			if _, err := o.IntegrationFabricsClient.Update(ctx, o.ResourceGroup, o.GrafanaName, operation.Name, parameters); err != nil {
				return err
			}
		case adxReconcileDelete:
			if err := o.IntegrationFabricsClient.Delete(ctx, o.ResourceGroup, o.GrafanaName, operation.Name); err != nil {
				return err
			}
		case adxReconcileRecreate:
			if err := o.IntegrationFabricsClient.Delete(ctx, o.ResourceGroup, o.GrafanaName, operation.Name); err != nil {
				return err
			}
			if _, err := o.IntegrationFabricsClient.Create(ctx, o.ResourceGroup, o.GrafanaName, operation.Name, integrationFabricResource(location, *operation.Desired)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported ADX integration fabric operation %q", operation.Type)
		}
	}

	return nil
}
