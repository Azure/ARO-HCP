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

package graph

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	kiotaabstractions "github.com/microsoft/kiota-abstractions-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func apiError(statusCode int) error {
	err := kiotaabstractions.NewApiError()
	err.SetStatusCode(statusCode)
	return err
}

func TestIsTransientOnlyCoversPropagationAndNetworkErrors(t *testing.T) {
	for _, statusCode := range []int{
		http.StatusBadRequest,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		assert.False(t, IsTransient(apiError(statusCode)), "status %d", statusCode)
	}

	assert.True(t, IsTransient(apiError(http.StatusNotFound)))
	assert.True(t, IsTransient(&net.DNSError{Err: "temporary failure", IsTemporary: true}))
	assert.True(t, IsTransient(fmt.Errorf("application %q is %w", "app", ErrNotVisible)))
	assert.False(t, IsTransient(errors.New("forbidden")))
}

func TestRetryReturnsOnceTheOperationSucceeds(t *testing.T) {
	attempts := 0
	result, err := Retry(context.Background(), time.Millisecond, func(context.Context) (string, error) {
		attempts++
		if attempts < 3 {
			return "", apiError(http.StatusNotFound)
		}
		return "found", nil
	})

	require.NoError(t, err)
	assert.Equal(t, "found", result)
	assert.Equal(t, 3, attempts)
}

func TestRetryStopsOnNonTransientError(t *testing.T) {
	attempts := 0
	err := RetryDo(context.Background(), time.Millisecond, func(context.Context) error {
		attempts++
		return errors.New("forbidden")
	})

	require.ErrorContains(t, err, "forbidden")
	assert.Equal(t, 1, attempts)
}

func TestRetryReportsLastTransientErrorWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := RetryDo(ctx, time.Millisecond, func(context.Context) error {
		return fmt.Errorf("application %q is %w", "app", ErrNotVisible)
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "application \"app\" is not visible in Microsoft Graph yet")
}

type fakePage struct {
	values []string
	next   *string
}

func (p *fakePage) GetValue() []string        { return p.values }
func (p *fakePage) GetOdataNextLink() *string { return p.next }

func TestCollectPagesFollowsEveryContinuationLink(t *testing.T) {
	link2, link3 := "page2", "page3"
	pages := map[string]*fakePage{
		link2: {values: []string{"c", "d"}, next: &link3},
		link3: {values: []string{"e"}},
	}
	var requested []string

	items, err := CollectPages(context.Background(), &fakePage{values: []string{"a", "b"}, next: &link2},
		func(_ context.Context, link string) (*fakePage, error) {
			requested = append(requested, link)
			return pages[link], nil
		})

	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c", "d", "e"}, items)
	assert.Equal(t, []string{"page2", "page3"}, requested)
}

func TestCollectPagesStopsOnPageError(t *testing.T) {
	link := "page2"

	_, err := CollectPages(context.Background(), &fakePage{values: []string{"a"}, next: &link},
		func(context.Context, string) (*fakePage, error) { return nil, errors.New("boom") })

	require.ErrorContains(t, err, "boom")
}
