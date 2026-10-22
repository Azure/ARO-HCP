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
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	cachetesting "k8s.io/client-go/tools/cache/testing"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	clusterdeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/deletion"
	clusteroperations "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/operations"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/mismatch"
	nodepooldeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/deletion"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosclient"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosratelimit"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/billingcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/kubeapplierinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
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
			ContainerThroughput: func(container string) (float64, error) {
				return map[string]float64{"Resources": 3.75, "Billing": 7.5, "Fleet": 15, "KubeApplier-test": 80, "KubeApplier-test2": 80}[container], nil
			},
			KubeApplierUtilization: 0.3,
			ControllerNames:        []string{"normal", "other", "cleanup"},
			ControllerFractions:    map[string]float64{"cleanup": 0.1},
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
		kubeApplier, err := factory.KubeApplierStorageClients("normal").For(ctx, mc)
		require.NoError(t, err)
		require.NotNil(t, kubeApplier, "its Fleet lookup uses that controller's Fleet budget")
		desires, err := kubeApplier.ReadDesiresForCluster("sub", "rg", "cluster")
		require.NoError(t, err)
		_, err = desires.GetByID(ctx, "item")
		require.True(t, cosmosstorageutils.IsNotFoundError(err), "%v", err)

		// Debt in one MC container must not consume the next MC's allocation.
		mc2, err := azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/2/managementClusters/default")
		require.NoError(t, err)
		kubeApplier2, err := factory.KubeApplierStorageClients("normal").For(ctx, mc2)
		require.NoError(t, err)
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
		ContainerThroughput: func(string) (float64, error) { return 10, nil },
		ControllerNames:     []string{"one"},
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
		{"missing throughput provider", func(o *StorageFactoryOptions) { o.ContainerThroughput = nil }},
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

func TestStorageFactoryDiscovery(t *testing.T) {
	options := BackendStorageFactoryOptions(false)
	var callsMu sync.Mutex
	calls := map[string]int{}
	blocked := make(chan struct{})
	release := make(chan struct{})
	options.ContainerThroughput = func(container string) (float64, error) {
		callsMu.Lock()
		calls[container]++
		attempt := calls[container]
		callsMu.Unlock()
		if container == "slow-mc" {
			close(blocked)
			<-release
			return 2000, nil
		}
		if container == "retry-mc" && attempt == 1 {
			return 0, fmt.Errorf("transient discovery failure")
		}
		return 1000, nil
	}
	factory, err := newStorageFactory("https://cosmos.test", "test", storageTestOptions(t, nil), options)
	require.NoError(t, err)
	controller := strings.ToLower(mismatch.MissingResourceIDControllerName)
	done := make(chan error, 1)
	go func() { _, err := factory.tokenBucket("slow-mc", controller); done <- err }()
	<-blocked
	fastDone := make(chan error, 1)
	go func() { _, err := factory.tokenBucket("fast-mc", controller); fastDone <- err }()
	select {
	case err := <-fastDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("discovery holds a shared lock")
	}
	close(release)
	require.NoError(t, <-done)
	_, err = factory.tokenBucket("retry-mc", controller)
	require.ErrorContains(t, err, "transient")
	require.NotContains(t, factory.rateLimits, "retry-mc")
	_, err = factory.tokenBucket("retry-mc", controller)
	require.NoError(t, err)
	var workers sync.WaitGroup
	errors := make(chan error, 20)
	for range 20 {
		workers.Go(func() { _, err := factory.tokenBucket("concurrent-mc", controller); errors <- err })
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.Equal(t, map[string]int{"Resources": 1, "Billing": 1, "Fleet": 1, "slow-mc": 1, "fast-mc": 1, "retry-mc": 2, "concurrent-mc": 1}, calls)
	require.NotSame(t, factory.rateLimits["slow-mc"], factory.rateLimits["fast-mc"])
}

func informerStorageTestOptions(t *testing.T) cosmosclient.Options {
	t.Helper()
	return storageTestOptions(t, func(req *http.Request) (*http.Response, error) {
		body := `{"Documents":[]}`
		status := http.StatusOK
		header := http.Header{}
		switch {
		case req.URL.Path == "" || req.URL.Path == "/":
			body = `{"id":"test","writableLocations":[{"name":"test","databaseAccountEndpoint":"https://cosmos.test"}],"readableLocations":[{"name":"test","databaseAccountEndpoint":"https://cosmos.test"}]}`
		case strings.Contains(req.URL.Path, "/colls/Fleet/"):
			body = `{"Documents":[{"id":"mc","partitionKey":"1","resourceID":"/providers/Microsoft.RedHatOpenShift/stamps/1/managementClusters/default","properties":{"status":{"kubeApplierCosmosContainerName":"KubeApplier-test"}}},{"id":"mc2","partitionKey":"2","resourceID":"/providers/Microsoft.RedHatOpenShift/stamps/2/managementClusters/default","properties":{"status":{"kubeApplierCosmosContainerName":"KubeApplier-other"}}}]}`
		case strings.HasSuffix(req.URL.Path, "/pkranges"):
			header.Set("ETag", "fixture")
			body = `{"PartitionKeyRanges":[{"id":"0","minInclusive":"","maxExclusive":"FF"}]}`
			if req.Header.Get("If-None-Match") != "" {
				status = http.StatusNotModified
				body = ""
			}
		case req.Method == http.MethodGet:
			status = http.StatusNotModified
			header.Set("ETag", "fixture")
			body = ""
		}
		return &http.Response{StatusCode: status, Request: req, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
}

func TestStorageFactoryStartsWithoutManagementClusters(t *testing.T) {
	options := BackendStorageFactoryOptions(false)
	var calls []string
	options.ContainerThroughput = func(container string) (float64, error) {
		calls = append(calls, container)
		return 1000, nil
	}
	clientOptions := informerStorageTestOptions(t)
	transport := clientOptions.Transport
	var registered atomic.Bool
	clientOptions.Transport = storageTestTransport(func(request *http.Request) (*http.Response, error) {
		if !registered.Load() && strings.Contains(request.URL.Path, "/colls/Fleet/") {
			return &http.Response{StatusCode: http.StatusOK, Request: request, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"Documents":[]}`))}, nil
		}
		return transport.Do(request)
	})
	factory, err := newStorageFactory("https://cosmos.test", "test", clientOptions, options)
	require.NoError(t, err)
	require.Equal(t, []string{"Resources", "Billing", "Fleet"}, calls)
	clients := factory.KubeApplierStorageClients(strings.ToLower(mismatch.MissingResourceIDControllerName))
	key := unionkubeapplierinformers.ManagementClusterKey{StampIdentifier: "1", ManagementClusterName: "default"}
	missing, err := clients.For(t.Context(), key.GetResourceID())
	require.Nil(t, missing)
	require.ErrorIs(t, err, kubeappliercosmosstorage.ErrManagementClusterNotFound)
	require.Equal(t, []string{"Resources", "Billing", "Fleet"}, calls)
	registered.Store(true)
	client, err := clients.For(t.Context(), key.GetResourceID())
	require.NoError(t, err)
	require.NotNil(t, client)
	cached, err := clients.For(t.Context(), key.GetResourceID())
	require.NoError(t, err)
	require.Same(t, client, cached)
	require.Equal(t, []string{"Resources", "Billing", "Fleet", "KubeApplier-test"}, calls)
}

func TestBackendKubeApplierInformerSkipsThroughputDiscovery(t *testing.T) {
	for _, realFPA := range []bool{false, true} {
		for _, discovery := range []string{"unconfigured", "same-container", "other-container"} {
			t.Run(fmt.Sprintf("realFPA=%t/%s", realFPA, discovery), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					options := BackendStorageFactoryOptions(realFPA)
					var calls atomic.Int32
					started, release := make(chan struct{}), make(chan struct{})
					options.ContainerThroughput = func(container string) (float64, error) {
						if container == "Resources" || container == "Billing" || container == "Fleet" {
							return 1000, nil
						}
						calls.Add(1)
						if discovery != "unconfigured" && container == "KubeApplier-test" {
							close(started)
							<-release
							return 1000, nil
						}
						return 0, fmt.Errorf("MC throughput is not configured")
					}
					factory, err := newStorageFactory("https://cosmos.test", "test", informerStorageTestOptions(t), options)
					require.NoError(t, err)
					key := unionkubeapplierinformers.ManagementClusterKey{StampIdentifier: "1", ManagementClusterName: "default"}
					limited := factory.KubeApplierStorageClients(strings.ToLower(mismatch.MissingResourceIDControllerName))
					limitedDone := make(chan kubeappliercosmosstorage.KubeApplierDBClient, 1)
					defer func() {
						close(release)
						if discovery != "unconfigured" {
							client := <-limitedDone
							require.NotNil(t, client)
							cached, err := limited.For(t.Context(), key.GetResourceID())
							require.NoError(t, err)
							require.Same(t, client, cached)
							require.EqualValues(t, 1, calls.Load(), "successful discovery is cached")
						}
					}()
					if discovery != "unconfigured" {
						go func() {
							client, err := limited.For(t.Context(), key.GetResourceID())
							if err != nil {
								t.Error(err)
							}
							limitedDone <- client
						}()
						<-started
					}
					informerKey := key
					if discovery == "other-container" {
						informerKey.StampIdentifier = "2"
					}
					informerFactory := unionkubeapplierinformers.NewKubeApplierInformerFactory(
						factory.KubeApplierStorageClients(unionkubeapplierinformers.ControllerName), nil)
					informerDone := make(chan kubeapplierinformers.KubeApplierInformers, 1)
					go func() {
						informerDone <- informerFactory.NewKubeApplierInformers(t.Context(), informerKey.GetResourceID())
					}()
					synctest.Wait()
					select {
					case informers := <-informerDone:
						require.NotNil(t, informers, "informer construction must not require MC throughput")
					default:
						t.Fatal("informer construction waits for a limited controller's throughput discovery")
					}
					if discovery == "unconfigured" {
						require.Zero(t, calls.Load(), "informers must not discover MC throughput")
					} else {
						require.EqualValues(t, 1, calls.Load(), "only the limited controller discovers throughput")
						require.Empty(t, limitedDone, "limited clients must wait for discovery before use")
					}
				})
			})
		}
	}
}

func TestBackendLimitedKubeApplierDiscoveryRetries(t *testing.T) {
	for _, failure := range []string{"error", "zero", "negative"} {
		t.Run(failure, func(t *testing.T) {
			options := BackendStorageFactoryOptions(false)
			calls := 0
			options.ContainerThroughput = func(container string) (float64, error) {
				if container != "KubeApplier-test" {
					return 1000, nil
				}
				calls++
				if calls == 1 {
					switch failure {
					case "error":
						return 0, fmt.Errorf("ARM denied")
					case "zero":
						return 0, nil
					case "negative":
						return -1, nil
					}
				}
				return 1000, nil
			}
			factory, err := newStorageFactory("https://cosmos.test", "test", informerStorageTestOptions(t), options)
			require.NoError(t, err)
			clients := factory.KubeApplierStorageClients(strings.ToLower(mismatch.MissingResourceIDControllerName))
			key := unionkubeapplierinformers.ManagementClusterKey{StampIdentifier: "1", ManagementClusterName: "default"}
			failed, err := clients.For(t.Context(), key.GetResourceID())
			require.Error(t, err)
			require.NotErrorIs(t, err, kubeappliercosmosstorage.ErrManagementClusterNotFound)
			require.Nil(t, failed, "failed discovery must not produce an unlimited client")
			require.Equal(t, 1, calls)
			require.NotContains(t, factory.rateLimits, "KubeApplier-test")
			client, err := clients.For(t.Context(), key.GetResourceID())
			require.NoError(t, err)
			require.NotNil(t, client)
			cached, err := clients.For(t.Context(), key.GetResourceID())
			require.NoError(t, err)
			require.Same(t, client, cached)
			require.Equal(t, 2, calls, "retry succeeds and subsequent lookups use the cache")
		})
	}
}

type trackingKubeApplierInformerFactory struct {
	unionkubeapplierinformers.PerMCKubeApplierInformerFactory
	created       atomic.Int32
	started       atomic.Int32
	stopped       atomic.Int32
	stoppedEvents chan struct{}
}

func (f *trackingKubeApplierInformerFactory) NewKubeApplierInformers(ctx context.Context, resourceID *azcorearm.ResourceID) kubeapplierinformers.KubeApplierInformers {
	informers := f.PerMCKubeApplierInformerFactory.NewKubeApplierInformers(ctx, resourceID)
	if informers == nil {
		return nil
	}
	f.created.Add(1)
	return &trackingKubeApplierInformers{KubeApplierInformers: informers, factory: f}
}

type trackingKubeApplierInformers struct {
	kubeapplierinformers.KubeApplierInformers
	factory *trackingKubeApplierInformerFactory
}

func (informers *trackingKubeApplierInformers) RunWithContext(ctx context.Context) {
	informers.factory.started.Add(1)
	defer func() {
		informers.factory.stopped.Add(1)
		informers.factory.stoppedEvents <- struct{}{}
	}()
	informers.KubeApplierInformers.RunWithContext(ctx)
}

func TestBackendKubeApplierInformerLifecycleDuringThroughputDiscovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		options := BackendStorageFactoryOptions(false)
		var calls atomic.Int32
		started, release := make(chan struct{}), make(chan struct{})
		releaseDiscovery := sync.OnceFunc(func() { close(release) })
		options.ContainerThroughput = func(container string) (float64, error) {
			if container == "Resources" || container == "Billing" || container == "Fleet" {
				return 1000, nil
			}
			calls.Add(1)
			close(started)
			<-release
			return 1000, nil
		}
		factory, err := newStorageFactory("https://cosmos.test", "test", informerStorageTestOptions(t), options)
		require.NoError(t, err)
		key := unionkubeapplierinformers.ManagementClusterKey{StampIdentifier: "1", ManagementClusterName: "default"}
		limited := factory.KubeApplierStorageClients(strings.ToLower(mismatch.MissingResourceIDControllerName))
		limitedDone := make(chan kubeappliercosmosstorage.KubeApplierDBClient, 1)
		go func() {
			client, err := limited.For(t.Context(), key.GetResourceID())
			if err != nil {
				t.Error(err)
			}
			limitedDone <- client
		}()
		defer func() {
			releaseDiscovery()
			require.NotNil(t, <-limitedDone)
		}()
		<-started
		tracked := &trackingKubeApplierInformerFactory{stoppedEvents: make(chan struct{}, 2), PerMCKubeApplierInformerFactory: unionkubeapplierinformers.NewKubeApplierInformerFactory(
			factory.KubeApplierStorageClients(unionkubeapplierinformers.ControllerName), nil)}
		source := cachetesting.NewFakeControllerSource()
		defer source.Shutdown()
		mcInformer := cache.NewSharedIndexInformer(source, &fleetapi.ManagementCluster{}, 0, nil)
		controller := unionkubeapplierinformers.NewUnionKubeApplierInformersController(
			mcInformer, fleetlisters.NewManagementClusterLister(mcInformer.GetIndexer()), tracked)
		ctx, cancel := context.WithCancel(t.Context())
		mcDone, controllerDone := make(chan struct{}), make(chan struct{})
		go func() { defer close(mcDone); mcInformer.RunWithContext(ctx) }()
		go func() { defer close(controllerDone); controller.Run(ctx, 1) }()
		defer func() { releaseDiscovery(); cancel(); <-controllerDone; <-mcDone }()
		require.True(t, cache.WaitForCacheSync(ctx.Done(), mcInformer.HasSynced))
		synctest.Wait()
		mc := &fleetapi.ManagementCluster{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: key.GetResourceID(), PartitionKey: "1"}}
		source.Add(mc.DeepCopyObject())
		synctest.Wait()
		require.EqualValues(t, 1, tracked.created.Load(), "addition constructs the real per-MC informers")
		require.EqualValues(t, 1, tracked.started.Load())
		source.Modify(mc.DeepCopyObject())
		synctest.Wait()
		require.EqualValues(t, 1, tracked.created.Load(), "duplicate events must not start duplicate informers")
		source.Delete(mc.DeepCopyObject())
		<-tracked.stoppedEvents
		synctest.Wait()
		require.EqualValues(t, 1, tracked.stopped.Load(), "removal cancels and joins the real informers")
		require.EqualValues(t, 1, tracked.created.Load(), "removal must not resurrect informers")
		source.Add(mc.DeepCopyObject())
		synctest.Wait()
		require.EqualValues(t, 2, tracked.created.Load())
		require.EqualValues(t, 2, tracked.started.Load())
		cancel()
		<-controllerDone
		<-mcDone
		require.EqualValues(t, 2, tracked.stopped.Load(), "shutdown joins all real informer goroutines")
		require.Empty(t, limitedDone, "all lifecycle events complete while same-container discovery remains blocked")
		require.EqualValues(t, 1, calls.Load(), "only the limited controller discovers throughput")
	})
}

func TestStorageFactoryDiscoveredMCBudgets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		options := StorageFactoryOptions{ControllerNames: []string{"controller"}, KubeApplierUtilization: 0.3,
			ContainerThroughput: func(container string) (float64, error) {
				if container == "large" {
					return 2000, nil
				}
				return 1000, nil
			},
		}
		factory, err := newStorageFactory("https://cosmos.test", "test", storageTestOptions(t, nil), options)
		require.NoError(t, err)
		for _, test := range []struct {
			name   string
			budget float64
		}{{"small", 300}, {"large", 600}, {"Resources", 800}} {
			bucket, err := factory.tokenBucket(test.name, "controller")
			require.NoError(t, err)
			bucket.Consume(2 * test.budget)
			start := time.Now()
			require.NoError(t, bucket.Wait(t.Context()))
			require.InDelta(t, float64(time.Second), float64(time.Since(start)), 1, test.name)
		}
	})
}

func TestStorageFactoryInitialDiscoveryFailure(t *testing.T) {
	for _, container := range []string{"Resources", "Billing", "Fleet"} {
		for _, failure := range []string{"error", "zero", "negative"} {
			t.Run(container+"/"+failure, func(t *testing.T) {
				options := BackendStorageFactoryOptions(false)
				var calls []string
				options.ContainerThroughput = func(name string) (float64, error) {
					calls = append(calls, name)
					if name == container {
						switch failure {
						case "error":
							return 0, fmt.Errorf("ARM denied")
						case "negative":
							return -1, nil
						default:
							return 0, nil
						}
					}
					return 1000, nil
				}
				factory, err := newStorageFactory("https://cosmos.test", "test", storageTestOptions(t, nil), options)
				require.Nil(t, factory)
				require.ErrorContains(t, err, container)
				require.Equal(t, container, calls[len(calls)-1])
			})
		}
	}
	_, err := newStorageFactory("https://cosmos.test", "test", storageTestOptions(t, nil), BackendStorageFactoryOptions(false))
	require.ErrorContains(t, err, "provider is required")
}

func TestBackendCleanupDiscoveryFailure(t *testing.T) {
	for _, scope := range []string{"cluster", "nodepool", "diagnostic"} {
		for _, failure := range []string{"ARM403", "ARM404", "timeout", "zero", "absent"} {
			t.Run(scope+"/"+failure, func(t *testing.T) {
				ctx := t.Context()
				require.True(t, utilruntime.ReallyCrash, "exercise production panic policy")
				fixture := operationtesting.NewClusterTestFixture()
				cluster := fixture.NewCluster(nil)
				now := metav1.Now()
				cluster.ServiceProviderProperties.ClusterServiceID = nil
				cluster.ServiceProviderProperties.DeletionTimestamp = &now
				cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &now
				deadline := metav1.NewTime(now.Add(-time.Hour))
				cluster.ServiceProviderProperties.DeleteOperationCompletionDeadline = &deadline
				mcID := metadataapi.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/1/managementClusters/default"))
				if failure == "absent" {
					mcID = metadataapi.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/1/managementClusters/unregistered"))
				}
				spc := &coreapi.ServiceProviderCluster{
					CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID(cluster.ID.String() + "/serviceProviderClusters/default")), PartitionKey: cluster.ID.SubscriptionID},
					Status:         coreapi.ServiceProviderClusterStatus{ManagementClusterResourceID: mcID},
				}
				nodepool := operationtesting.NewNodePoolTestFixture().NewNodePool()
				nodepool.ServiceProviderProperties.ClusterServiceID = nil
				nodepool.ServiceProviderProperties.DeletionTimestamp = &now
				nodepool.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &now
				spnp := &coreapi.ServiceProviderNodePool{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID(nodepool.ID.String() + "/serviceProviderNodePools/default")), PartitionKey: cluster.ID.SubscriptionID}}
				operation := fixture.NewOperation(cosmosstorageutils.OperationRequestDelete)
				resources := []any{cluster, spc, operation}
				if scope == "nodepool" {
					resources = append(resources, nodepool, spnp)
				}
				db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
				require.NoError(t, err)
				billing := billingcosmosstoragetesting.NewMockBillingDBClient()
				informers := coreinformers.NewBackendInformers(ctx, db.ResourcesGlobalListers(), db, billing.BillingGlobalListers())
				clusterInformer, _ := informers.Clusters()
				require.NoError(t, clusterInformer.GetIndexer().Add(cluster))
				nodepoolInformer, _ := informers.NodePools()
				require.NoError(t, nodepoolInformer.GetIndexer().Add(nodepool))
				options := BackendStorageFactoryOptions(false)
				calls := 0
				recovered, hasDesires := false, true
				options.ContainerThroughput = func(container string) (float64, error) {
					if container != "KubeApplier-test" {
						return 100000, nil
					}
					calls++
					if recovered {
						return 100000, nil
					}
					switch failure {
					case "ARM403":
						return 0, &azcore.ResponseError{StatusCode: http.StatusForbidden}
					case "ARM404":
						return 0, &azcore.ResponseError{StatusCode: http.StatusNotFound}
					case "timeout":
						return 0, context.DeadlineExceeded
					default:
						return 0, nil
					}
				}
				parent := cluster.ID
				if scope == "nodepool" {
					parent = nodepool.ID
				}
				desireID := metadataapi.Must(azcorearm.ParseResourceID(parent.String() + "/applyDesires/owned"))
				desire, err := json.Marshal(cosmosstorageutils.GenericDocument[kubeapplierapi.ApplyDesire]{
					TypedDocument: cosmosstorageutils.TypedDocument{
						BaseDocument: cosmosstorageutils.BaseDocument{ID: "owned"},
						ResourceID:   desireID, ResourceType: strings.ToLower(desireID.ResourceType.String()), PartitionKey: strings.ToLower(mcID.String()),
					},
					Content: kubeapplierapi.ApplyDesire{Tags: map[string]string{kubeapplierapi.TagControllerName: "owner"}},
				})
				require.NoError(t, err)
				dataOptions := informerStorageTestOptions(t)
				originalTransport := dataOptions.Transport
				dataOptions.Transport = storageTestTransport(func(request *http.Request) (*http.Response, error) {
					if !strings.Contains(request.URL.Path, "/colls/KubeApplier-test/docs") {
						return originalTransport.Do(request)
					}
					body := string(desire)
					switch request.Method {
					case http.MethodPost:
						body = `{"Documents":[]}`
						if hasDesires {
							body = `{"Documents":[` + string(desire) + `]}`
						}
					case http.MethodGet:
					default:
						return nil, fmt.Errorf("unexpected desire mutation: %s", request.Method)
					}
					return &http.Response{StatusCode: http.StatusOK, Request: request, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
				})
				factory, err := newStorageFactory("https://cosmos.test", "test", dataOptions, options)
				require.NoError(t, err)
				var controller controllerutils.Controller
				var key any
				switch scope {
				case "cluster":
					clients := factory.KubeApplierStorageClients(strings.ToLower(clusterdeletion.ClusterChildResourcesCleanupControllerControllerName))
					controller = clusterdeletion.NewClusterChildResourcesCleanupController(db, clients, informers, nil)
					key = controllerutils.HCPClusterKey{SubscriptionID: cluster.ID.SubscriptionID, ResourceGroupName: cluster.ID.ResourceGroupName, HCPClusterName: cluster.ID.Name}
				case "nodepool":
					clients := factory.KubeApplierStorageClients(strings.ToLower(nodepooldeletion.NodePoolChildResourcesCleanupControllerControllerName))
					controller = nodepooldeletion.NewNodePoolChildResourcesCleanupController(db, clients, informers, nil)
					key = controllerutils.HCPNodePoolKey{SubscriptionID: cluster.ID.SubscriptionID, ResourceGroupName: cluster.ID.ResourceGroupName, HCPClusterName: cluster.ID.Name, HCPNodePoolName: nodepool.ID.Name}
				default:
					clients := factory.KubeApplierStorageClients(strings.ToLower(clusteroperations.OperationClusterDeleteControllerName))
					active, _ := informers.ActiveOperations()
					controller = clusteroperations.NewOperationClusterDeleteController(clocktesting.NewFakeClock(now.Time), db, billing, clients, &kubeapplierlistertesting.SliceReadDesireLister{}, &kubeapplierlistertesting.SliceApplyDesireLister{}, nil, nil, active)
					key = fixture.OperationKey()
				}
				t.Cleanup(func() {
					stopped, cancel := context.WithCancel(context.Background())
					cancel()
					controller.Run(stopped, 0)
				})
				require.NotPanics(t, func() { err = controller.SyncOnce(ctx, key) })
				if scope == "diagnostic" {
					require.NoError(t, err)
					updated, err := db.Operations(cluster.ID.SubscriptionID).Get(ctx, operation.ResourceID.Name)
					require.NoError(t, err)
					require.Equal(t, coreapi.ProvisioningStateFailed, updated.Status)
					require.Contains(t, updated.Error.Message, "diagnostics incomplete")
				} else if failure == "absent" {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
					require.False(t, errors.Is(err, kubeappliercosmosstorage.ErrManagementClusterNotFound))
				}
				if failure == "absent" {
					require.Zero(t, calls, "no ARM discovery for an unregistered MC")
					switch scope {
					case "cluster":
						_, err = db.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name).Get(ctx, "default")
						require.True(t, cosmosstorageutils.IsNotFoundError(err))
					case "nodepool":
						_, err = db.ServiceProviderNodePools(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name, nodepool.ID.Name).Get(ctx, "default")
						require.True(t, cosmosstorageutils.IsNotFoundError(err))
					}
					return
				}
				require.Equal(t, 1, calls, "the actual limited factory must attempt ARM discovery")
				_, err = db.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name).Get(ctx, "default")
				require.NoError(t, err, "SPC must survive incomplete inventory")
				if scope == "nodepool" {
					_, err = db.ServiceProviderNodePools(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name, nodepool.ID.Name).Get(ctx, "default")
					require.NoError(t, err, "SPNP must survive incomplete inventory")
				}
				if scope != "diagnostic" {
					recovered = true
					require.NoError(t, controller.SyncOnce(ctx, key))
					if scope == "cluster" {
						_, err = db.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name).Get(ctx, "default")
					} else {
						_, err = db.ServiceProviderNodePools(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name, nodepool.ID.Name).Get(ctx, "default")
					}
					require.NoError(t, err, "recovery must still respect remaining desires")
					hasDesires = false
					require.NoError(t, controller.SyncOnce(ctx, key))
					if scope == "cluster" {
						_, err = db.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name).Get(ctx, "default")
					} else {
						_, err = db.ServiceProviderNodePools(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name, nodepool.ID.Name).Get(ctx, "default")
					}
					require.True(t, cosmosstorageutils.IsNotFoundError(err), "only confirmed empty inventory permits deletion")
					require.Equal(t, 2, calls, "discovery retries once then caches success")
				}
			})
		}
	}
}

func TestBackendStorageRegistrations(t *testing.T) {
	for _, realFPA := range []bool{false, true} {
		names := BackendStorageControllerNames(realFPA)
		options := storageTestOptions(t, func(*http.Request) (*http.Response, error) {
			t.Fatal("startup must not contact Cosmos")
			return nil, nil
		})
		factoryOptions := BackendStorageFactoryOptions(realFPA)
		factoryOptions.ContainerThroughput = func(string) (float64, error) { return 1000, nil }
		factory, err := newStorageFactory("https://cosmos.test", "test", options, factoryOptions)
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
		options.ContainerThroughput = func(string) (float64, error) { return 1000, nil }
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
		budget := 1000 * cosmosratelimit.DefaultControllerUtilization /
			float64(len(options.ControllerNames)-len(options.UnlimitedControllerNames))
		bucket, err := factory.tokenBucket("Resources", strings.ToLower(mismatch.MissingResourceIDControllerName))
		require.NoError(t, err)
		bucket.Consume(2 * budget)
		start := time.Now()
		require.NoError(t, bucket.Wait(t.Context()))
		require.InDelta(t, float64(time.Second), float64(time.Since(start)), 1, "controllers retain their budget and throttling")
	})
}
