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

package dns

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	armhelpers "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/arm"
)

const DNSZonesResourceType = "Microsoft.Network/dnszones"
const NSRecordSetResourceType = "Microsoft.Network/dnszones/NS"

type DeleteNSDelegationRecordsStep struct {
	runner.DeletionStep
}

type DeleteNSDelegationRecordsStepConfig struct {
	ResourceGroupName string
	Credential        azcore.TokenCredential
	ResourcesClient   *armresources.Client
	SubsClient        *armsubscriptions.Client

	Name            string
	Retries         int
	ContinueOnError bool
	Verify          runner.VerifyFn
}

var _ runner.StepOptionsProvider = DeleteNSDelegationRecordsStepConfig{}

func (c DeleteNSDelegationRecordsStepConfig) StepOptions() runner.StepOptions {
	return runner.StepOptions{
		Name:            c.Name,
		Retries:         c.Retries,
		ContinueOnError: c.ContinueOnError,
		Verify:          c.Verify,
	}
}

func NewDeleteNSDelegationRecordsStep(cfg DeleteNSDelegationRecordsStepConfig) *DeleteNSDelegationRecordsStep {
	stepOptions := cfg.StepOptions()
	if stepOptions.Name == "" {
		stepOptions.Name = "Delete parent NS delegations"
	}

	step := &DeleteNSDelegationRecordsStep{
		DeletionStep: runner.DeletionStep{
			ResourceType: NSRecordSetResourceType,
			Options:      stepOptions,
		},
	}

	step.DiscoverFn = func(ctx context.Context, _ string) ([]runner.Target, error) {
		logger := runner.LoggerFromContext(ctx)
		childZones, err := armhelpers.ListByType(ctx, cfg.ResourcesClient, cfg.ResourceGroupName, DNSZonesResourceType)
		if err != nil {
			return nil, err
		}
		targets := make([]runner.Target, 0, len(childZones))
		for _, resource := range childZones {
			if resource == nil || resource.Name == nil {
				continue
			}
			childZone := *resource.Name
			parentZone, recordSetName, ok := parseDelegation(childZone)
			if !ok {
				continue
			}

			delegationTargets, err := discoverNSDelegationRecordTargets(ctx, cfg.Credential, cfg.SubsClient, parentZone, recordSetName)
			if err != nil {
				logger.Info(
					fmt.Sprintf("[WARNING] Failed NS delegation discovery for child zone %q; continuing", childZone),
					"parentZone", parentZone,
					"recordSetName", recordSetName,
					"error", err,
				)
			}
			targets = append(targets, delegationTargets...)
		}
		return targets, nil
	}

	step.DeleteFn = func(ctx context.Context, target runner.Target, _ bool) error {
		subscriptionID, resourceGroup, zoneName, recordSetName, err := parseNSRecordSetTargetID(target.ID)
		if err != nil {
			return err
		}
		return deleteNSRecordSet(ctx, cfg.Credential, subscriptionID, resourceGroup, zoneName, recordSetName)
	}

	step.VerifyFn = func(ctx context.Context) error {
		if stepOptions.Verify == nil {
			return nil
		}
		return stepOptions.Verify(ctx)
	}

	return step
}

func discoverNSDelegationRecordTargets(ctx context.Context, credential azcore.TokenCredential, subsClient *armsubscriptions.Client, parentZone, recordSetName string) ([]runner.Target, error) {
	targets := []runner.Target{}
	var errs []error
	subsPager := subsClient.NewListPager(nil)
	for subsPager.More() {
		page, err := subsPager.NextPage(ctx)
		if err != nil {
			wrappedErr := fmt.Errorf("failed listing subscriptions while cleaning NS records for parent zone %q: %w", parentZone, err)
			errs = append(errs, wrappedErr)
			continue
		}

		for _, sub := range page.Value {
			if sub.SubscriptionID == nil {
				continue
			}

			subID := *sub.SubscriptionID
			dnsClient, err := armdns.NewZonesClient(subID, credential, nil)
			if err != nil {
				wrappedErr := fmt.Errorf("failed creating DNS zones client for subscription %q: %w", subID, err)
				errs = append(errs, wrappedErr)
				continue
			}
			recordSetsClient, err := armdns.NewRecordSetsClient(subID, credential, nil)
			if err != nil {
				wrappedErr := fmt.Errorf("failed creating DNS record-sets client for subscription %q: %w", subID, err)
				errs = append(errs, wrappedErr)
				continue
			}

			zonesPager := dnsClient.NewListPager(nil)
			for zonesPager.More() {
				zonePage, err := zonesPager.NextPage(ctx)
				if err != nil {
					wrappedErr := fmt.Errorf("failed listing zones in subscription %q while looking for parent zone %q: %w", subID, parentZone, err)
					errs = append(errs, wrappedErr)
					continue
				}

				for _, zone := range zonePage.Value {
					if zone == nil || zone.Name == nil || zone.ID == nil {
						continue
					}
					if !strings.EqualFold(*zone.Name, parentZone) {
						continue
					}

					parsedID, err := arm.ParseResourceID(*zone.ID)
					if err != nil {
						wrappedErr := fmt.Errorf("failed parsing zone ID %q: %w", *zone.ID, err)
						errs = append(errs, wrappedErr)
						continue
					}
					if parsedID.ResourceGroupName == "" {
						wrappedErr := fmt.Errorf("zone ID %q does not include a resource group", *zone.ID)
						errs = append(errs, wrappedErr)
						continue
					}

					recordSetID := buildNSRecordSetID(subID, parsedID.ResourceGroupName, parentZone, recordSetName)
					if _, err := recordSetsClient.Get(ctx, parsedID.ResourceGroupName, parentZone, recordSetName, armdns.RecordTypeNS, nil); err != nil {
						wrappedErr := fmt.Errorf("failed getting NS record-set %q in zone %q (%s/%s): %w", recordSetName, parentZone, subID, parsedID.ResourceGroupName, err)
						errs = append(errs, wrappedErr)
						continue
					}
					targets = append(targets, runner.Target{
						ID:   recordSetID,
						Name: parentZone + "/" + recordSetName,
						Type: NSRecordSetResourceType,
					})
				}
			}
		}
	}

	return targets, errors.Join(errs...)
}

func deleteNSRecordSet(ctx context.Context, credential azcore.TokenCredential, subscriptionID, resourceGroup, zoneName, recordSetName string) error {
	recordSetsClient, err := armdns.NewRecordSetsClient(subscriptionID, credential, nil)
	if err != nil {
		return err
	}

	_, err = recordSetsClient.Delete(ctx, resourceGroup, zoneName, recordSetName, armdns.RecordTypeNS, nil)
	return err
}

func parseDelegation(childZone string) (parentZone string, recordSetName string, ok bool) {
	parts := strings.Split(childZone, ".")
	if len(parts) <= 2 {
		return "", "", false
	}
	return strings.Join(parts[1:], "."), parts[0], true
}

func buildNSRecordSetID(subscriptionID, resourceGroup, zoneName, recordSetName string) string {
	return "/subscriptions/" + subscriptionID +
		"/resourceGroups/" + resourceGroup +
		"/providers/Microsoft.Network/dnszones/" + zoneName +
		"/NS/" + recordSetName
}

func parseNSRecordSetTargetID(id string) (subscriptionID, resourceGroup, zoneName, recordSetName string, err error) {
	parsed, err := arm.ParseResourceID(id)
	if err != nil {
		return "", "", "", "", err
	}
	if parsed.SubscriptionID == "" || parsed.ResourceGroupName == "" {
		return "", "", "", "", fmt.Errorf("invalid NS record-set target ID: %s", id)
	}

	// For an ID shaped as:
	// /subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.Network/dnszones/<zone>/NS/<recordSet>
	// ParseResourceID sets parsed.Name to the leaf resource name (<recordSet>), so we need to read <zone>
	// from the parent segment.
	recordSetName = strings.TrimSpace(parsed.Name)
	if recordSetName == "" {
		recordSetName = strings.TrimSpace(extractResourceIDSegmentValue(id, "NS"))
	}
	if recordSetName == "" {
		return "", "", "", "", fmt.Errorf("invalid NS record-set target name in ID: %s", id)
	}

	if parsed.Parent != nil {
		zoneName = strings.TrimSpace(parsed.Parent.Name)
	}
	if zoneName == "" {
		zoneName = strings.TrimSpace(extractResourceIDSegmentValue(id, "dnszones"))
	}
	if zoneName == "" {
		return "", "", "", "", fmt.Errorf("invalid NS record-set target name in ID: %s", id)
	}

	return parsed.SubscriptionID, parsed.ResourceGroupName, zoneName, recordSetName, nil
}

func extractResourceIDSegmentValue(resourceID, key string) string {
	parts := strings.Split(strings.Trim(resourceID, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if strings.EqualFold(parts[i], key) {
			return parts[i+1]
		}
	}
	return ""
}
