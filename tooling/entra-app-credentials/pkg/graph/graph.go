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

// Package graph holds read-only identity discovery and Microsoft Graph helpers
// shared by credential reconcilers: token scope, OData escaping, pagination and
// retry of lookups while a freshly created object is still replicating.
package graph

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	kiotaabstractions "github.com/microsoft/kiota-abstractions-go"
	msgraph "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/microsoftgraph/msgraph-sdk-go/applications"
	"github.com/microsoftgraph/msgraph-sdk-go/applicationswithuniquename"
	"github.com/microsoftgraph/msgraph-sdk-go/models"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
)

const (
	// Scope is the token scope requested for Microsoft Graph.
	Scope = "https://graph.microsoft.com/.default"
	// DefaultRetryInterval is how long Retry waits between attempts by default.
	DefaultRetryInterval = 2 * time.Second
)

// ErrNotVisible reports that an object is not visible in Microsoft Graph yet.
// Retry treats it as transient.
var ErrNotVisible = errors.New("not visible in Microsoft Graph yet")

// QuoteFilterValue escapes a value for use inside a single-quoted OData string.
func QuoteFilterValue(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

// IsNotFound reports whether err is a Microsoft Graph 404 response.
func IsNotFound(err error) bool {
	var apiError kiotaabstractions.ApiErrorable
	return errors.As(err, &apiError) && apiError.GetStatusCode() == http.StatusNotFound
}

// LookupApplication reads an application by its immutable unique name and checks
// its IDs and expected display name. The unique name need not be the display name.
// Credential metadata is included for callers checking safe initial preparation.
func LookupApplication(ctx context.Context, client *msgraph.GraphServiceClient, uniqueName, expectedDisplayName string) (models.Applicationable, error) {
	application, err := client.ApplicationsWithUniqueName(&uniqueName).Get(ctx, &applicationswithuniquename.ApplicationsWithUniqueNameRequestBuilderGetRequestConfiguration{
		QueryParameters: &applicationswithuniquename.ApplicationsWithUniqueNameRequestBuilderGetQueryParameters{Select: []string{"id", "appId", "displayName", "keyCredentials"}},
	})
	if err != nil {
		return nil, fmt.Errorf("read application uniqueName %q: %w", uniqueName, err)
	}
	if application == nil || !validID(application.GetId()) || !validID(application.GetAppId()) || ptr.Deref(application.GetDisplayName(), "") != expectedDisplayName {
		return nil, fmt.Errorf("application uniqueName %q has unexpected identity or invalid identity IDs", uniqueName)
	}
	return application, nil
}

// LookupServicePrincipal reads the service principal bound to appID, rejecting
// malformed IDs, disabled accounts and a response bound to a different app.
func LookupServicePrincipal(ctx context.Context, client *msgraph.GraphServiceClient, appID string) (models.ServicePrincipalable, error) {
	if !validID(&appID) {
		return nil, fmt.Errorf("service principal application ID %q is invalid", appID)
	}
	principal, err := client.ServicePrincipalsWithAppId(&appID).Get(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("read service principal for application %s: %w", appID, err)
	}
	if principal == nil || !validID(principal.GetId()) || ptr.Deref(principal.GetAppId(), "") != appID || !ptr.Deref(principal.GetAccountEnabled(), false) {
		return nil, fmt.Errorf("service principal for application %s is missing, disabled or mismatched, or has an invalid identity ID", appID)
	}
	return principal, nil
}

// CheckApplicationDisplayName requires displayName to identify exactly the
// expected object. An empty expectedObjectID instead requires the name be absent.
func CheckApplicationDisplayName(ctx context.Context, client *msgraph.GraphServiceClient, displayName, expectedObjectID string) error {
	first, err := client.Applications().Get(ctx, &applications.ApplicationsRequestBuilderGetRequestConfiguration{
		QueryParameters: &applications.ApplicationsRequestBuilderGetQueryParameters{Filter: ptr.To("displayName eq '" + QuoteFilterValue(displayName) + "'"), Select: []string{"id"}},
	})
	if err != nil {
		return fmt.Errorf("check application displayName %q: %w", displayName, err)
	}
	if first == nil || first.GetValue() == nil {
		return fmt.Errorf("application displayName %q returned no collection", displayName)
	}
	matches, err := CollectPages(ctx, first, func(ctx context.Context, link string) (models.ApplicationCollectionResponseable, error) {
		page, err := client.Applications().WithUrl(link).Get(ctx, nil)
		if err == nil && (page == nil || page.GetValue() == nil) {
			return nil, fmt.Errorf("application displayName %q returned no collection", displayName)
		}
		return page, err
	})
	if err != nil {
		return fmt.Errorf("list application displayName %q: %w", displayName, err)
	}
	if expectedObjectID == "" && len(matches) == 0 {
		return nil
	}
	if expectedObjectID != "" && len(matches) == 1 && matches[0] != nil && ptr.Deref(matches[0].GetId(), "") == expectedObjectID {
		return nil
	}
	return fmt.Errorf("application displayName %q missing or ambiguous (%d matches); expected object absent or name belongs to another uniqueName", displayName, len(matches))
}

func validID(value *string) bool {
	id, err := uuid.Parse(ptr.Deref(value, ""))
	return err == nil && id != uuid.Nil
}

// IsTransient reports whether err is worth retrying. Kiota retries its own
// retryable HTTP statuses, so only propagation delays (404, ErrNotVisible) and
// transport failures that escape the SDK middleware qualify.
func IsTransient(err error) bool {
	if errors.Is(err, ErrNotVisible) {
		return true
	}

	var apiError kiotaabstractions.ApiErrorable
	if errors.As(err, &apiError) {
		return apiError.GetStatusCode() == http.StatusNotFound
	}

	var networkError net.Error
	return errors.As(err, &networkError)
}

// Retry runs operation every interval until it succeeds, fails with a
// non-transient error, or ctx ends.
func Retry[T any](ctx context.Context, interval time.Duration, operation func(context.Context) (T, error)) (T, error) {
	var result T
	var lastErr error
	err := wait.PollUntilContextCancel(ctx, interval, true, func(ctx context.Context) (bool, error) {
		var err error
		result, err = operation(ctx)
		switch {
		case err == nil:
			return true, nil
		case IsTransient(err):
			lastErr = err
			return false, nil
		default:
			return false, err
		}
	})

	switch {
	case err == nil:
		return result, nil
	case lastErr != nil && ctx.Err() != nil:
		return result, fmt.Errorf("retry Microsoft Graph operation: %w (last error: %v)", ctx.Err(), lastErr)
	default:
		return result, err
	}
}

// RetryDo is Retry for operations without a result.
func RetryDo(ctx context.Context, interval time.Duration, operation func(context.Context) error) error {
	_, err := Retry(ctx, interval, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, operation(ctx)
	})
	return err
}

// Page is one page of a Microsoft Graph collection response.
type Page[T any] interface {
	GetValue() []T
	GetOdataNextLink() *string
}

// CollectPages returns the items of first and of every page reachable through
// its @odata.nextLink continuation links. next fetches the page at a link.
func CollectPages[T any, P Page[T]](ctx context.Context, first P, next func(ctx context.Context, link string) (P, error)) ([]T, error) {
	var items []T
	page := first
	for {
		items = append(items, page.GetValue()...)
		link := page.GetOdataNextLink()
		if link == nil || *link == "" {
			return items, nil
		}
		var err error
		page, err = next(ctx, *link)
		if err != nil {
			return nil, err
		}
	}
}
