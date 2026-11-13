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

package framework

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcehealth/armresourcehealth"
)

// NewAvailabilityStatusesClient builds an Azure Resource Health client for
// reading the availability status of an ARM resource.
func NewAvailabilityStatusesClient(subscriptionID string, cred azcore.TokenCredential) (*armresourcehealth.AvailabilityStatusesClient, error) {
	client, err := armresourcehealth.NewAvailabilityStatusesClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource health availability statuses client for subscription %s: %w", subscriptionID, err)
	}
	return client, nil
}

// GetAvailabilityStatus returns the current Azure Resource Health availability
// status for an ARM resource — the same data the Resource Health blade shows.
//
// A 404 here almost always means the Microsoft.ResourceHealth provider is not
// registered on the subscription rather than "the resource is not healthy yet",
// so that case is called out explicitly: a caller polling for Available would
// otherwise burn its whole timeout on a misconfiguration.
func GetAvailabilityStatus(ctx context.Context, client *armresourcehealth.AvailabilityStatusesClient, resourceID string) (*armresourcehealth.AvailabilityStatus, error) {
	resp, err := client.GetByResource(ctx, resourceID, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("resource health returned 404 for %s (is the Microsoft.ResourceHealth provider registered on this subscription?): %w", resourceID, err)
		}
		return nil, fmt.Errorf("failed to get resource health availability status for %s: %w", resourceID, err)
	}
	return &resp.AvailabilityStatus, nil
}

// FormatAvailabilityStatus renders an availability status as a single compact
// line for delta-only poll logging and failure messages. It reports what was
// actually observed, so a reader can tell Unknown (nothing ever reported) from
// Degraded/Unavailable (reported and bad) without reading the test source.
func FormatAvailabilityStatus(status *armresourcehealth.AvailabilityStatus) string {
	if status == nil || status.Properties == nil {
		return "<no availability status>"
	}

	props := status.Properties
	parts := []string{fmt.Sprintf("state=%s", derefOr(props.AvailabilityState, "<nil>"))}
	if props.ReasonType != nil && *props.ReasonType != "" {
		parts = append(parts, fmt.Sprintf("reason=%s", *props.ReasonType))
	}
	if props.Summary != nil && *props.Summary != "" {
		parts = append(parts, fmt.Sprintf("summary=%q", *props.Summary))
	}
	if props.ReportedTime != nil {
		parts = append(parts, fmt.Sprintf("reported=%s", props.ReportedTime.UTC().Format("2006-01-02T15:04:05Z")))
	}
	if props.OccurredTime != nil {
		parts = append(parts, fmt.Sprintf("occurred=%s", props.OccurredTime.UTC().Format("2006-01-02T15:04:05Z")))
	}
	return strings.Join(parts, " ")
}

// derefOr renders a *AvailabilityStateValues, falling back to a placeholder
// when it is nil. The SDK documents a nil AvailabilityState as meaning the
// object describes an impacting event rather than a state, so it must not be
// printed as an empty string.
func derefOr(state *armresourcehealth.AvailabilityStateValues, fallback string) string {
	if state == nil {
		return fallback
	}
	return string(*state)
}
