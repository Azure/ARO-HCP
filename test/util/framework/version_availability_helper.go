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
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

// WaitForOpenShiftVersionAvailable20240610 waits for an upstream-resolvable minor
// version and channel group to be advertised consistently by the regional LIST
// and GET endpoints.
// Its separate budget also bounds SDK retries, Retry-After delays and body reads.
func WaitForOpenShiftVersionAvailable20240610(ctx context.Context, client *hcpsdk20240610preview.HcpOpenShiftVersionsClient, location string, profile coreapi.VersionProfile) error {
	availabilityCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	return waitForOpenShiftVersionAvailable20240610(availabilityCtx, client, location, profile, StandardPollInterval)
}

func waitForOpenShiftVersionAvailable20240610(ctx context.Context, client *hcpsdk20240610preview.HcpOpenShiftVersionsClient, location string, profile coreapi.VersionProfile, interval time.Duration) error {
	// Keep this public API expectation independent of the production naming policy.
	name := profile.ID
	if profile.ChannelGroup != "stable" {
		name += "-" + profile.ChannelGroup
	}
	lastStatus := "not polled"
	var observedNames []string
	handleError := func(operation string, err error) (bool, error) {
		var responseError *azcore.ResponseError
		if errors.As(err, &responseError) {
			lastStatus = fmt.Sprintf("%s HTTP %d (%s)", operation, responseError.StatusCode, responseError.ErrorCode)
		} else {
			lastStatus = fmt.Sprintf("%s: %v", operation, err)
		}
		// Body decoding can wrap cancellation or replace it with a parse error.
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if responseError != nil {
			status := responseError.StatusCode
			if status == http.StatusNotFound || status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500 && status < 600 {
				return false, nil
			}
			return false, err
		}
		var urlError *url.Error
		if errors.As(err, &urlError) && urlError.Op == "parse" {
			return false, err
		}
		var networkError net.Error
		if errors.As(err, &networkError) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, nil
		}
		// The pinned SDK flattens JSON decoding errors with %s/%v, losing their
		// types. Fail unclassified errors, including decoding and request setup,
		// rather than retrying a deterministic failure for the entire budget.
		return false, err
	}
	err := wait.PollUntilContextCancel(ctx, interval, true, func(ctx context.Context) (bool, error) {
		observedNames = nil
		var listed *hcpsdk20240610preview.HcpOpenShiftVersion
		// Never reuse a completed or failed pager on the next poll.
		pager := client.NewListPager(location, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return handleError("LIST", err)
			}
			for _, version := range page.Value {
				if version == nil {
					continue
				}
				observedName := ptr.Deref(version.Name, "<nil>")
				observedNames = append(observedNames, observedName)
				if observedName == name {
					listed = version
				}
			}
		}
		lastStatus = "LIST HTTP 200; expected version absent"
		if listed == nil {
			return false, nil
		}
		if err := validateAvailableOpenShiftVersion20240610(listed, name, profile.ChannelGroup); err != nil {
			lastStatus = "LIST HTTP 200; " + err.Error()
			return false, err
		}
		got, err := client.Get(ctx, location, name, nil)
		if err != nil {
			return handleError("GET", err)
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		lastStatus = "LIST and GET HTTP 200"
		if err := validateAvailableOpenShiftVersion20240610(&got.HcpOpenShiftVersion, name, profile.ChannelGroup); err != nil {
			lastStatus += "; GET " + err.Error()
			return false, err
		}
		if diff := cmp.Diff(*listed, got.HcpOpenShiftVersion); diff != "" {
			return false, fmt.Errorf("LIST and GET disagree for %q (-LIST +GET):\n%s", name, diff)
		}
		return true, nil
	})
	if err != nil {
		const maxObservedNames = 12
		omitted := 0
		if len(observedNames) > maxObservedNames {
			omitted = len(observedNames) - maxObservedNames
			observedNames = observedNames[:maxObservedNames]
		}
		return fmt.Errorf("waiting for OpenShift version %q (minor=%q channelGroup=%q) in location %q: last status: %s; observed names=%v (%d omitted): %w", name, profile.ID, profile.ChannelGroup, location, lastStatus, observedNames, omitted, err)
	}
	return nil
}

func validateAvailableOpenShiftVersion20240610(version *hcpsdk20240610preview.HcpOpenShiftVersion, name, group string) error {
	observedName, observedGroup, observedEnabled := "<nil>", "<nil>", "<nil>"
	if version != nil {
		observedName = ptr.Deref(version.Name, "<nil>")
		if version.Properties != nil {
			observedGroup = ptr.Deref(version.Properties.ChannelGroup, "<nil>")
			if version.Properties.Enabled != nil {
				observedEnabled = fmt.Sprint(*version.Properties.Enabled)
			}
		}
	}
	if observedName != name || observedGroup != group || observedEnabled != "true" {
		return fmt.Errorf("expected name=%q channelGroup=%q enabled=true; observed name=%q channelGroup=%q enabled=%s", name, group, observedName, observedGroup, observedEnabled)
	}
	return nil
}
