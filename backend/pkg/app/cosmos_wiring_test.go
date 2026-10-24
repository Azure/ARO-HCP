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
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/billing"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/mismatch"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosclient"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosratelimit"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
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
		{"unknown unlimited controller", func(o *StorageFactoryOptions) { o.UnlimitedControllerNames = []string{"typo"} }},
		{"duplicate unlimited controller", func(o *StorageFactoryOptions) { o.UnlimitedControllerNames = []string{"one", "one"} }},
		{"unlimited controller fraction", func(o *StorageFactoryOptions) {
			o.ControllerNames = []string{"one", "two"}
			o.UnlimitedControllerNames = []string{"one"}
			o.ControllerFractions = map[string]float64{"one": .1}
		}},
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

type billingPatchRequest struct {
	Method, Path, Continuation string
	Body                       billingPatchBody
}

type billingPatchBody struct {
	Query      string                  `json:"query,omitempty"`
	Parameters []billingQueryParameter `json:"parameters,omitempty"`
	Condition  string                  `json:"condition,omitempty"`
	Operations []billingPatchOperation `json:"operations,omitempty"`
}

type billingQueryParameter struct{ Name, Value string }
type billingPatchOperation struct {
	Op, Path string
	Value    time.Time
}
type billingPatchExchange struct {
	Want               billingPatchRequest
	Status             int
	Body, Continuation string
}
type billingPatchCall struct {
	Cutoff     time.Time
	WantError  string
	WantStatus int
}

func TestBillingPatchRequestsAndErrors(t *testing.T) {
	resourceID, parseErr := azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster")
	require.NoError(t, parseErr)
	firstCutoff := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	secondCutoff := firstCutoff.Add(time.Hour)
	const documentsPath = "/dbs/test/colls/Billing/docs"
	query := func(body, sentContinuation, nextContinuation string, status int) billingPatchExchange {
		return billingPatchExchange{
			Want: billingPatchRequest{Method: http.MethodPost, Path: documentsPath, Continuation: sentContinuation,
				Body: billingPatchBody{
					Query:      "SELECT c.id FROM c WHERE STRINGEQUALS(c.resourceId, @resourceId, true) AND (NOT IS_DEFINED(c.deletionTime) OR IS_NULL(c.deletionTime))",
					Parameters: []billingQueryParameter{{Name: "@resourceId", Value: resourceID.String()}},
				}},
			Status: status, Body: body, Continuation: nextContinuation,
		}
	}
	patch := func(id string, cutoff time.Time, status int) billingPatchExchange {
		return billingPatchExchange{
			Want: billingPatchRequest{Method: http.MethodPatch, Path: documentsPath + "/" + id,
				Body: billingPatchBody{
					Condition:  "FROM c WHERE NOT IS_DEFINED(c.deletionTime) OR IS_NULL(c.deletionTime)",
					Operations: []billingPatchOperation{{Op: "set", Path: "/deletionTime", Value: cutoff}},
				}},
			Status: status, Body: `{}`,
		}
	}
	for _, tc := range []struct {
		name      string
		direct    bool
		exchanges []billingPatchExchange
		calls     []billingPatchCall
	}{
		{name: "unset cutoff condition sent", direct: true, calls: []billingPatchCall{{Cutoff: firstCutoff}},
			exchanges: []billingPatchExchange{patch("doc", firstCutoff, http.StatusOK)}},
		{name: "condition failure propagated", direct: true, calls: []billingPatchCall{{Cutoff: firstCutoff, WantStatus: http.StatusPreconditionFailed}},
			exchanges: []billingPatchExchange{patch("doc", firstCutoff, http.StatusPreconditionFailed)}},
		{name: "missing projected ID refuses patch", calls: []billingPatchCall{{Cutoff: firstCutoff, WantError: "missing ID"}},
			exchanges: []billingPatchExchange{query(`{"Documents":[{}]}`, "", "", http.StatusOK)}},
		{name: "partial success retries only remaining active record", calls: []billingPatchCall{
			{Cutoff: firstCutoff, WantStatus: http.StatusPreconditionFailed}, {Cutoff: secondCutoff},
		}, exchanges: []billingPatchExchange{
			query(`{"Documents":[{"id":"a"},{"id":"b"}]}`, "", "", http.StatusOK),
			patch("a", firstCutoff, http.StatusOK),
			patch("b", firstCutoff, http.StatusPreconditionFailed),
			query(`{"Documents":[{"id":"b"}]}`, "", "", http.StatusOK),
			patch("b", secondCutoff, http.StatusOK),
		}},
		{name: "later query page failure sends no patches", calls: []billingPatchCall{{Cutoff: firstCutoff, WantStatus: http.StatusBadRequest}}, exchanges: []billingPatchExchange{
			query(`{"Documents":[{"id":"a"}]}`, "", "next-page", http.StatusOK),
			query(`{"code":"BadRequest","message":"page failed"}`, "next-page", "", http.StatusBadRequest),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			options := storageTestOptions(t, func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "" || req.URL.Path == "/" {
					return &http.Response{StatusCode: http.StatusOK, Request: req, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"test","writableLocations":[{"name":"test","databaseAccountEndpoint":"https://cosmos.test"}],"readableLocations":[{"name":"test","databaseAccountEndpoint":"https://cosmos.test"}]}`))}, nil
				}
				require.Less(t, requests, len(tc.exchanges), "unexpected request %s %s", req.Method, req.URL.Path)
				exchange := tc.exchanges[requests]
				requests++
				body, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				got := billingPatchRequest{Method: req.Method, Path: req.URL.Path, Continuation: req.Header.Get("x-ms-continuation")}
				require.NoError(t, json.Unmarshal(body, &got.Body))
				if diff := cmp.Diff(exchange.Want, got); diff != "" {
					t.Fatalf("request %d (-want +got):\n%s", requests, diff)
				}
				headers := http.Header{}
				if exchange.Continuation != "" {
					headers.Set("x-ms-continuation", exchange.Continuation)
				}
				return &http.Response{StatusCode: exchange.Status, Request: req, Header: headers, Body: io.NopCloser(strings.NewReader(exchange.Body))}, nil
			})
			factory, err := newStorageFactory("https://cosmos.test", "test", options, BackendStorageFactoryOptions(false))
			require.NoError(t, err)
			crud := factory.BillingStorageClient("orphanedbillingcleanup").BillingDocs("sub")
			for _, call := range tc.calls {
				var operations billingcosmosstorage.BillingDocumentPatchOperations
				operations.SetDeletionTime(call.Cutoff)
				if tc.direct {
					err = crud.PatchByID(t.Context(), "doc", operations)
				} else {
					err = crud.PatchByClusterID(t.Context(), resourceID, operations)
				}
				switch {
				case call.WantError != "":
					require.ErrorContains(t, err, call.WantError)
				case call.WantStatus != 0:
					var responseErr *azcore.ResponseError
					require.ErrorAs(t, err, &responseErr)
					require.Equal(t, call.WantStatus, responseErr.StatusCode)
				default:
					require.NoError(t, err)
				}
			}
			require.Equal(t, len(tc.exchanges), requests)
		})
	}
}

func TestBackendStorageRegistrations(t *testing.T) {
	for _, realFPA := range []bool{false, true} {
		names := BackendStorageControllerNames(realFPA)
		require.Contains(t, names, strings.ToLower(billing.ReconcileBillingManagedResourceGroupControllerName))
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

func TestBackendInformerStorageIsUnlimited(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		options := BackendStorageFactoryOptions(false)
		require.ElementsMatch(t, []string{
			BackendInformersStorageName,
			FleetInformersStorageName,
			unionkubeapplierinformers.ControllerName,
		}, options.UnlimitedControllerNames)
		factory, err := newStorageFactory("https://cosmos.test", "test", storageTestOptions(t, nil), options)
		require.NoError(t, err)
		for _, name := range options.UnlimitedControllerNames {
			for _, container := range []string{"Resources", "Billing", "Fleet", "new-mc"} {
				bucket, err := factory.tokenBucket(container, name)
				require.NoError(t, err)
				bucket.Consume(1e9)
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				start := time.Now()
				require.NoError(t, bucket.Wait(ctx), "%s/%s must not throttle informer reads", name, container)
				require.Equal(t, start, time.Now())
				cancel()
			}
		}
		// Unlimited informers must not reduce the normal controllers' allocation.
		budget := options.ResourcesRUsPerSecond * cosmosratelimit.DefaultControllerUtilization /
			float64(len(options.ControllerNames)-len(options.UnlimitedControllerNames))
		bucket, err := factory.tokenBucket("Resources", strings.ToLower(mismatch.MissingResourceIDControllerName))
		require.NoError(t, err)
		bucket.Consume(2 * budget)
		start := time.Now()
		require.NoError(t, bucket.Wait(t.Context()))
		require.InDelta(t, float64(time.Second), float64(time.Since(start)), 1, "controllers retain their budget and throttling")
	})
}
