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

// Package graph holds the Microsoft Graph helpers shared by the credential
// reconcilers: the token scope, OData escaping and retry of lookups that fail
// while a freshly created object is still replicating.
package graph

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	kiotaabstractions "github.com/microsoft/kiota-abstractions-go"

	"k8s.io/apimachinery/pkg/util/wait"
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
