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

package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosclient"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosratelimit"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

type storageTestTransport func(*http.Request) (*http.Response, error)

func (f storageTestTransport) Do(req *http.Request) (*http.Response, error) { return f(req) }

func storageTestOptions(t *testing.T, transport storageTestTransport) cosmosclient.Options {
	t.Helper()
	credential, err := azcosmos.NewKeyCredential("ZmFrZQ==")
	require.NoError(t, err)
	return cosmosclient.Options{
		KeyCredential: &credential,
		ClientOptions: azcore.ClientOptions{
			Retry:     policy.RetryOptions{MaxRetries: -1},
			Transport: transport,
		},
	}
}

func TestStorageFactorySeparatesContainerAndControllerBudgets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := time.Now()
		var attempts []time.Duration
		var containers []string
		options := storageTestOptions(t, func(req *http.Request) (*http.Response, error) {
			// SDK account discovery has no RU cost.
			if req.URL.Path == "" || req.URL.Path == "/" {
				return &http.Response{StatusCode: http.StatusOK, Request: req, Header: http.Header{},
					Body: io.NopCloser(strings.NewReader(`{"id":"test","writableLocations":[{"name":"test","databaseAccountEndpoint":"https://cosmos.test"}],"readableLocations":[{"name":"test","databaseAccountEndpoint":"https://cosmos.test"}]}`))}, nil
			}
			attempts = append(attempts, time.Since(started))
			containers = append(containers, strings.Split(req.URL.Path, "/")[4])
			if strings.EqualFold(req.Header.Get("x-ms-documentdb-query"), "true") {
				return &http.Response{StatusCode: http.StatusOK, Request: req,
					Header: http.Header{"X-Ms-Request-Charge": []string{"2"}},
					Body:   io.NopCloser(strings.NewReader(`{"Documents":[{"id":"mc","partitionKey":"1","resourceID":"/providers/Microsoft.RedHatOpenShift/stamps/1/managementClusters/default","properties":{"status":{"kubeApplierCosmosContainerName":"KubeApplier-test"}}},{"id":"mc2","partitionKey":"2","resourceID":"/providers/Microsoft.RedHatOpenShift/stamps/2/managementClusters/default","properties":{"status":{"kubeApplierCosmosContainerName":"KubeApplier-test2"}}}]}`))}, nil
			}
			charge := "2"
			if strings.Contains(req.URL.Path, "/colls/KubeApplier-") {
				charge = "16"
			}
			return &http.Response{
				StatusCode: http.StatusNotFound, Request: req,
				Header: http.Header{"X-Ms-Request-Charge": []string{charge}},
				Body:   io.NopCloser(strings.NewReader(`{"code":"NotFound","message":"fixture"}`)),
			}, nil
		})
		factory, err := newStorageFactory("https://cosmos.test", "test", options, StorageFactoryOptions{
			ResourcesRUsPerSecond:   3.75, // 1 RU per controller after 80% utilization.
			BillingRUsPerSecond:     7.5,  // 2 RUs per controller.
			FleetRUsPerSecond:       15,   // 4 RUs per controller.
			KubeApplierRUsPerSecond: 80,   // 8 RUs per controller at 30% utilization.
			KubeApplierUtilization:  0.3,
			ControllerNames:         []string{"normal", "other", "cleanup"},
			ControllerFractions:     map[string]float64{"cleanup": 0.1},
		})
		require.NoError(t, err)
		require.Same(t, factory.ResourcesStorageClient("normal"), factory.ResourcesStorageClient("normal"))
		require.Same(t, factory.BillingStorageClient("normal"), factory.BillingStorageClient("normal"))
		require.Same(t, factory.FleetStorageClient("normal"), factory.FleetStorageClient("normal"))
		require.Same(t, factory.KubeApplierStorageClients("normal"), factory.KubeApplierStorageClients("normal"))

		// No controller identity or limiter is carried in this context. The
		// selected client's pipeline owns its budget, including failed reads.
		ctx := t.Context()
		_, err = factory.ResourcesStorageClient("normal").HCPClusters("sub", "rg").GetByID(ctx, "item")
		require.True(t, cosmosstorageutils.IsNotFoundError(err), "%v", err)
		_, err = factory.ResourcesStorageClient("other").HCPClusters("sub", "rg").GetByID(ctx, "item")
		require.True(t, cosmosstorageutils.IsNotFoundError(err), "%v", err)
		_, err = factory.BillingStorageClient("normal").BillingDocs("sub").GetByID(ctx, "item")
		require.True(t, cosmosstorageutils.IsNotFoundError(err), "%v", err)
		_, err = factory.FleetStorageClient("normal").Stamps().ManagementClusters("1").GetByID(ctx, "item")
		require.True(t, cosmosstorageutils.IsNotFoundError(err), "%v", err)
		mc, err := azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/1/managementClusters/default")
		require.NoError(t, err)
		kubeApplier := factory.KubeApplierStorageClients("normal").For(ctx, mc)
		require.NotNil(t, kubeApplier, "its Fleet lookup uses that controller's Fleet budget")
		desires, err := kubeApplier.ReadDesiresForCluster("sub", "rg", "cluster")
		require.NoError(t, err)
		_, err = desires.GetByID(ctx, "item")
		require.True(t, cosmosstorageutils.IsNotFoundError(err), "%v", err)

		// Debt in one MC container must not consume the next MC's allocation.
		mc2, err := azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/2/managementClusters/default")
		require.NoError(t, err)
		kubeApplier2 := factory.KubeApplierStorageClients("normal").For(ctx, mc2)
		require.NotNil(t, kubeApplier2)
		desires2, err := kubeApplier2.ReadDesiresForCluster("sub", "rg", "cluster")
		require.NoError(t, err)
		_, err = desires2.GetByID(ctx, "item")
		require.True(t, cosmosstorageutils.IsNotFoundError(err), "%v", err)
		require.Equal(t, time.Duration(0), time.Since(started), "other containers and controllers must remain independent")
		_, err = desires.GetByID(ctx, "item")
		require.True(t, cosmosstorageutils.IsNotFoundError(err), "%v", err)
		require.Equal(t, time.Second, time.Since(started), "the same MC bucket must repay its own debt")

		transaction := factory.ResourcesStorageClient("normal").NewTransaction("sub")
		transaction.AddStep(cosmosstorageutils.CosmosDBTransactionStepDetails{}, func(batch *azcosmos.TransactionalBatch) (string, error) {
			batch.ReadItem("item", nil)
			return "item", nil
		})
		_, err = transaction.Execute(ctx, nil)
		require.True(t, cosmosstorageutils.IsNotFoundError(err), "%v", err)
		// A fresh CRUD must retain the Resources debt from the transaction.
		_, err = factory.ResourcesStorageClient("normal").HCPClusters("sub", "rg").GetByID(ctx, "item")
		require.True(t, cosmosstorageutils.IsNotFoundError(err), "%v", err)
		require.Equal(t, []time.Duration{0, 0, 0, 0, 0, 0, 0, 0, time.Second, time.Second, 3 * time.Second}, attempts)
		require.Equal(t, []string{"Resources", "Resources", "Billing", "Fleet", "Fleet", "KubeApplier-test", "Fleet", "KubeApplier-test2", "KubeApplier-test", "Resources", "Resources"}, containers)

		// Cleanup discounts apply independently to every container's capacity
		// and refill rate, including lazily created MC budgets.
		for container, normalBudget := range map[string]float64{"Resources": 1, "Billing": 2, "Fleet": 4, "KubeApplier-test": 8} {
			cleanup, err := factory.tokenBucket(container, "cleanup")
			require.NoError(t, err)
			cleanup.Consume(normalBudget * 0.2)
			before := time.Now()
			require.NoError(t, cleanup.Wait(ctx))
			require.Equal(t, time.Second, time.Since(before), container)
		}

		// Cancellation prevents dispatch even through a newly obtained CRUD.
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, err = factory.ResourcesStorageClient("normal").HCPClusters("sub", "rg").GetByID(canceled, "item")
		require.ErrorIs(t, err, context.Canceled)
		require.Len(t, attempts, 11)
	})
}

func testStorageAllocations() StorageFactoryOptions {
	return StorageFactoryOptions{
		ResourcesRUsPerSecond: 10, BillingRUsPerSecond: 10,
		FleetRUsPerSecond: 10, KubeApplierRUsPerSecond: 10,
		ControllerNames: []string{"one"},
	}
}

func TestStorageFactoryRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*StorageFactoryOptions)
	}{
		{"empty", func(o *StorageFactoryOptions) { o.ControllerNames = nil }},
		{"missing name", func(o *StorageFactoryOptions) { o.ControllerNames = []string{""} }},
		{"duplicate", func(o *StorageFactoryOptions) { o.ControllerNames = []string{"one", "one"} }},
		{"unknown override", func(o *StorageFactoryOptions) { o.ControllerFractions = map[string]float64{"typo": .1} }},
		{"invalid Resources budget", func(o *StorageFactoryOptions) { o.ResourcesRUsPerSecond = 0 }},
		{"invalid Billing budget", func(o *StorageFactoryOptions) { o.BillingRUsPerSecond = 0 }},
		{"invalid Fleet budget", func(o *StorageFactoryOptions) { o.FleetRUsPerSecond = 0 }},
		{"invalid kube-applier budget", func(o *StorageFactoryOptions) { o.KubeApplierRUsPerSecond = 0 }},
		{"invalid kube-applier utilization", func(o *StorageFactoryOptions) { o.KubeApplierUtilization = 1.1 }},
		{"invalid fraction", func(o *StorageFactoryOptions) { o.ControllerFractions = map[string]float64{"one": 0} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := testStorageAllocations()
			tc.mutate(&options)
			_, err := newStorageFactory("https://cosmos.test", "test", storageTestOptions(t, nil), options)
			require.Error(t, err)
		})
	}
	_, err := newStorageFactory(":invalid URL", "test", storageTestOptions(t, nil), testStorageAllocations())
	require.ErrorContains(t, err, `controller "one"`)
}

func TestStorageFactoryConcurrentMCContainerLookups(t *testing.T) {
	factory, err := newStorageFactory("https://cosmos.test", "test", storageTestOptions(t, nil), testStorageAllocations())
	require.NoError(t, err)
	buckets := make([]*cosmosratelimit.TokenBucket, 20)
	errors := make([]error, len(buckets))
	var wg sync.WaitGroup
	for i := range buckets {
		wg.Go(func() { buckets[i], errors[i] = factory.tokenBucket("new-mc", "one") })
	}
	wg.Wait()
	for i := range buckets {
		require.NoError(t, errors[i])
		require.Same(t, buckets[0], buckets[i])
	}
	other, err := factory.tokenBucket("another-mc", "one")
	require.NoError(t, err)
	require.NotSame(t, buckets[0], other)
}

func TestBackendStorageRegistrations(t *testing.T) {
	for _, realFPA := range []bool{false, true} {
		names := BackendStorageControllerNames(realFPA)
		options := storageTestOptions(t, func(*http.Request) (*http.Response, error) {
			t.Fatal("startup must not contact Cosmos")
			return nil, nil
		})
		factory, err := newStorageFactory("https://cosmos.test", "test", options, BackendStorageFactoryOptions(realFPA))
		require.NoError(t, err)
		for _, name := range names {
			require.NotNil(t, factory.ResourcesStorageClient(name))
			require.NotNil(t, factory.BillingStorageClient(name))
			require.NotNil(t, factory.FleetStorageClient(name))
			require.NotNil(t, factory.KubeApplierStorageClients(name))
		}
		require.PanicsWithValue(t, `unregistered storage controller "typo"`, func() { factory.ResourcesStorageClient("typo") })
	}
}
