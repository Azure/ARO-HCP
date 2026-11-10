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
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

const experimentSpecName = "Cluster Etcd Managed HSM Encryption should create a cluster with Managed HSM keyVaultType"

func TestSpecSelectsDirectResourceGroupDeletion(t *testing.T) {
	testCases := []struct {
		name     string
		specName string
		rawSpecs string
		want     bool
	}{
		{name: "empty selector keeps the standard path", specName: experimentSpecName, rawSpecs: "", want: false},
		{name: "no matching fragment", specName: experimentSpecName, rawSpecs: "Customer should be able to create a nodepool", want: false},
		{name: "exact spec name", specName: experimentSpecName, rawSpecs: experimentSpecName, want: true},
		{name: "normalized fragment with punctuation", specName: experimentSpecName, rawSpecs: "cluster-etcd-managed-hsm", want: true},
		{name: "case insensitive fragment", specName: experimentSpecName, rawSpecs: "CLUSTER ETCD MANAGED HSM", want: true},
		{name: "one of several fragments", specName: experimentSpecName, rawSpecs: "other spec, managed hsm, another", want: true},
		{name: "empty fragments are ignored", specName: experimentSpecName, rawSpecs: " , ,", want: false},
		{name: "empty spec name", specName: "", rawSpecs: "anything", want: false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, specSelectsDirectResourceGroupDeletion(tc.specName, tc.rawSpecs))
		})
	}
}

func TestClassifyResourceLookup(t *testing.T) {
	notFound := &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "ResourceNotFound"}
	forbidden := &azcore.ResponseError{StatusCode: http.StatusForbidden, ErrorCode: "AuthorizationFailed"}
	transport := errors.New("connection reset")

	testCases := []struct {
		name      string
		err       error
		wantGone  bool
		wantFatal bool
	}{
		{name: "present resource returns no error", err: nil, wantGone: false, wantFatal: false},
		{name: "typed 404 means gone", err: notFound, wantGone: true, wantFatal: false},
		{name: "wrapped typed 404 means gone", err: errors.Join(errors.New("get failed"), notFound), wantGone: true, wantFatal: false},
		{name: "403 is fatal", err: forbidden, wantGone: false, wantFatal: true},
		{name: "transport error is fatal", err: transport, wantGone: false, wantFatal: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gone, fatal := classifyResourceLookup(tc.err)
			assert.Equal(t, tc.wantGone, gone)
			if tc.wantFatal {
				require.Error(t, fatal)
				return
			}
			require.NoError(t, fatal)
		})
	}
}

func TestWaitForResourceDisappearance(t *testing.T) {
	notFound := &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "ResourceNotFound"}

	t.Run("no resources", func(t *testing.T) {
		surviving, err := waitForResourceDisappearance(context.Background(), time.Second, time.Millisecond, nil)
		require.NoError(t, err)
		assert.Empty(t, surviving)
	})

	t.Run("all resources gone immediately", func(t *testing.T) {
		probes := map[string]func(context.Context) error{
			"resource group rg-a":          func(context.Context) error { return notFound },
			"managed resource group mrg-b": func(context.Context) error { return notFound },
		}
		surviving, err := waitForResourceDisappearance(context.Background(), time.Second, time.Millisecond, probes)
		require.NoError(t, err)
		assert.Empty(t, surviving)
	})

	t.Run("resource disappears before the timeout", func(t *testing.T) {
		calls := 0
		probes := map[string]func(context.Context) error{
			"cluster rg-a/c1": func(context.Context) error {
				calls++
				if calls < 3 {
					return nil
				}
				return notFound
			},
		}
		surviving, err := waitForResourceDisappearance(context.Background(), 5*time.Second, time.Millisecond, probes)
		require.NoError(t, err)
		assert.Empty(t, surviving)
		assert.GreaterOrEqual(t, calls, 3)
	})

	t.Run("surviving resources fail with sorted names", func(t *testing.T) {
		probes := map[string]func(context.Context) error{
			"managed resource group mrg-b": func(context.Context) error { return nil },
			"cluster rg-a/c1":              func(context.Context) error { return nil },
			"resource group rg-a":          func(context.Context) error { return notFound },
		}
		surviving, err := waitForResourceDisappearance(context.Background(), 20*time.Millisecond, time.Millisecond, probes)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "resources still present")
		assert.Equal(t, []string{"cluster rg-a/c1", "managed resource group mrg-b"}, surviving)
	})

	t.Run("permission error aborts the wait", func(t *testing.T) {
		probes := map[string]func(context.Context) error{
			"resource group rg-a": func(context.Context) error {
				return &azcore.ResponseError{StatusCode: http.StatusForbidden, ErrorCode: "AuthorizationFailed"}
			},
		}
		start := time.Now()
		surviving, err := waitForResourceDisappearance(context.Background(), 5*time.Second, time.Millisecond, probes)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "AuthorizationFailed")
		assert.Empty(t, surviving)
		assert.Less(t, time.Since(start), time.Second, "a fatal lookup error must not wait for the timeout")
	})

	t.Run("interrupted lookup reports the unverified resources", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		probes := map[string]func(context.Context) error{
			"cluster rg-a/c1": func(context.Context) error {
				cancel()
				return ctx.Err()
			},
			"resource group rg-a": func(context.Context) error { return nil },
		}
		surviving, err := waitForResourceDisappearance(ctx, 5*time.Second, time.Millisecond, probes)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not verified before the context ended")
		assert.Equal(t, []string{"cluster rg-a/c1", "resource group rg-a"}, surviving)
	})
}

func TestMergeResourceNames(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "c"}, mergeResourceNames([]string{"c", "a"}, []string{"b", "a", " "}, []string{""}))
	assert.Empty(t, mergeResourceNames(nil, []string{""}))
	assert.Equal(t, []string{"mrg-1"}, mergeResourceNames([]string{"mrg-1"}))
}
