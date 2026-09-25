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

package integrationutils

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

// WaitForFrontendCaches waits for visible DB membership, versions, and content,
// not just initial synchronization. Fixture loads need not advance versions.
func (i *IntegrationTestInfo) WaitForFrontendCaches(ctx context.Context) error {
	var pending string
	err := wait.PollUntilContextTimeout(ctx, 20*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		var err error
		pending, err = frontendCacheDifference(ctx, i.ResourcesDBClient().ResourcesGlobalListers().Clusters(), i.ClusterInformer)
		if err != nil || pending != "" {
			return false, err
		}
		pending, err = frontendCacheDifference(ctx, i.ResourcesDBClient().ResourcesGlobalListers().NodePools(), i.NodePoolInformer)
		return pending == "", err
	})
	if err != nil {
		return fmt.Errorf("waiting for frontend caches (%s): %w", pending, err)
	}
	return nil
}

func frontendCacheDifference[T any, P coreapi.CosmosMetadataAccessorPtr[T]](ctx context.Context, lister cosmosstorageutils.GlobalLister[T], informer cache.SharedIndexInformer) (string, error) {
	if !informer.HasSynced() {
		return fmt.Sprintf("%T cache has not synced", new(T)), nil
	}
	iter, err := lister.List(ctx, nil)
	if err != nil {
		return "", err
	}
	expected := map[string]*T{}
	for _, item := range iter.Items(ctx) {
		expected[strings.ToLower(P(item).GetResourceID().String())] = item
	}
	if err := iter.GetError(); err != nil {
		return "", err
	}
	actual := informer.GetStore().List()
	if len(actual) != len(expected) {
		return fmt.Sprintf("%T membership: cache has %d, DB has %d", new(T), len(actual), len(expected)), nil
	}
	for _, item := range actual {
		cached := item.(P)
		key := strings.ToLower(cached.GetResourceID().String())
		stored, ok := expected[key]
		if !ok {
			return fmt.Sprintf("cache contains deleted resource %s", key), nil
		}
		// Compare serialized typed objects to avoid private ResourceID fields and
		// retain all admission-relevant content as well as instanceVersion.
		want, err := json.Marshal(stored)
		if err != nil {
			return "", err
		}
		got, err := json.Marshal(cached)
		if err != nil {
			return "", err
		}
		if string(want) != string(got) {
			return fmt.Sprintf("stale resource %s: cache version %d, DB version %d; content differs", key, cached.GetInstanceVersion(), P(stored).GetInstanceVersion()), nil
		}
	}
	return "", nil
}

// WaitForHTTPReady requires a healthy response, not merely an open listener.
func WaitForHTTPReady(ctx context.Context, urls ...string) error {
	client := &http.Client{Timeout: time.Second}
	var pending string
	err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		for _, url := range urls {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return false, err
			}
			response, err := client.Do(request)
			if err != nil {
				pending = fmt.Sprintf("%s: %v", url, err)
				return false, nil
			}
			if err := response.Body.Close(); err != nil {
				return false, err
			}
			if response.StatusCode != http.StatusOK {
				pending = fmt.Sprintf("%s: %s", url, response.Status)
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("waiting for HTTP readiness (%s): %w", pending, err)
	}
	return nil
}
