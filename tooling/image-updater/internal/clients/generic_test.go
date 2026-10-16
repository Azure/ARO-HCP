// Copyright 2025 Microsoft Corporation
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

package clients

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

func TestParseNextLink(t *testing.T) {
	tests := []struct {
		name        string
		linkHeader  string
		registryURL string
		want        string
	}{
		{
			name:        "relative path is resolved against registryURL",
			linkHeader:  `</v2/repo/tags/list?n=100&last=tag>; rel="next"`,
			registryURL: "quay.io",
			want:        "https://quay.io/v2/repo/tags/list?n=100&last=tag",
		},
		{
			name:        "absolute URL on the same host is followed",
			linkHeader:  `<https://quay.io/v2/repo/tags/list?n=100&last=tag>; rel="next"`,
			registryURL: "quay.io",
			want:        "https://quay.io/v2/repo/tags/list?n=100&last=tag",
		},
		{
			name:        "absolute URL on a different host is rejected",
			linkHeader:  `<https://evil.example.com/v2/repo/tags/list?n=100&last=tag>; rel="next"`,
			registryURL: "quay.io",
			want:        "",
		},
		{
			name:        "absolute URL downgrading to http is rejected",
			linkHeader:  `<http://quay.io/v2/repo/tags/list?n=100&last=tag>; rel="next"`,
			registryURL: "quay.io",
			want:        "",
		},
		{
			name:        "no rel=next entry returns empty",
			linkHeader:  `</v2/repo/tags/list?n=100&last=tag>; rel="prev"`,
			registryURL: "quay.io",
			want:        "",
		},
		{
			name:        "empty header returns empty",
			linkHeader:  "",
			registryURL: "quay.io",
			want:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseNextLink(tt.linkHeader, tt.registryURL)
			if got != tt.want {
				t.Errorf("parseNextLink(%q, %q) = %q, want %q", tt.linkHeader, tt.registryURL, got, tt.want)
			}
		})
	}
}

func TestFetchTagMetadataConcurrentlyPreservesInputOrder(t *testing.T) {
	secondFinished := make(chan struct{})
	tags := []Tag{{Name: "first"}, {Name: "second"}}

	metadata, err := fetchTagMetadataConcurrently(context.Background(), tags, func(_ context.Context, tag Tag) (fetchedTagMetadata, error) {
		if tag.Name == "first" {
			<-secondFinished
		} else {
			close(secondFinished)
		}
		return fetchedTagMetadata{tag: tag}, nil
	})
	if err != nil {
		t.Fatalf("fetchTagMetadataConcurrently() unexpected error = %v", err)
	}
	if metadata[0].tag.Name != "first" || metadata[1].tag.Name != "second" {
		t.Fatalf("metadata order = %q, %q; want first, second", metadata[0].tag.Name, metadata[1].tag.Name)
	}
}

func TestFetchTagMetadataConcurrentlyBoundsConcurrency(t *testing.T) {
	const tagCount = maxConcurrentRepositoryMetadataRequests + 2
	tags := make([]Tag, tagCount)
	for i := range tags {
		tags[i].Name = fmt.Sprintf("tag-%d", i)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, tagCount)
	release := make(chan struct{}, tagCount)
	var active atomic.Int64
	var maximum atomic.Int64
	result := make(chan error, 1)
	go func() {
		_, err := fetchTagMetadataConcurrently(ctx, tags, func(ctx context.Context, tag Tag) (fetchedTagMetadata, error) {
			current := active.Add(1)
			defer active.Add(-1)
			for observed := maximum.Load(); current > observed && !maximum.CompareAndSwap(observed, current); observed = maximum.Load() {
			}
			select {
			case started <- struct{}{}:
			case <-ctx.Done():
				return fetchedTagMetadata{}, ctx.Err()
			}
			select {
			case <-release:
				return fetchedTagMetadata{tag: tag}, nil
			case <-ctx.Done():
				return fetchedTagMetadata{}, ctx.Err()
			}
		})
		result <- err
	}()

	for i := 0; i < maxConcurrentRepositoryMetadataRequests; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for metadata workers")
		}
	}
	select {
	case <-started:
		t.Fatalf("metadata concurrency exceeded limit %d", maxConcurrentRepositoryMetadataRequests)
	default:
	}
	release <- struct{}{}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("queued metadata worker did not start after capacity became available")
	}
	for i := 0; i < tagCount; i++ {
		release <- struct{}{}
	}
	if err := <-result; err != nil {
		t.Fatalf("fetchTagMetadataConcurrently() unexpected error = %v", err)
	}
	if got := maximum.Load(); got != maxConcurrentRepositoryMetadataRequests {
		t.Fatalf("maximum metadata concurrency = %d, want %d", got, maxConcurrentRepositoryMetadataRequests)
	}
}

func TestFetchTagMetadataConcurrentlyFailsClosed(t *testing.T) {
	metadataErr := errors.New("metadata unavailable")
	_, err := fetchTagMetadataConcurrently(context.Background(), []Tag{{Name: "latest"}}, func(_ context.Context, _ Tag) (fetchedTagMetadata, error) {
		return fetchedTagMetadata{}, metadataErr
	})
	if !errors.Is(err, metadataErr) {
		t.Fatalf("fetchTagMetadataConcurrently() error = %v, want metadata failure", err)
	}
}

func TestFetchTagMetadataConcurrentlyHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fetchTagMetadataConcurrently(ctx, []Tag{{Name: "latest"}}, func(ctx context.Context, _ Tag) (fetchedTagMetadata, error) {
		<-ctx.Done()
		return fetchedTagMetadata{}, ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("fetchTagMetadataConcurrently() error = %v, want context cancellation", err)
	}
}

func TestFetchTagMetadataConcurrentlyConvertsPanicToError(t *testing.T) {
	previousReallyCrash := utilruntime.ReallyCrash
	utilruntime.ReallyCrash = false
	defer func() { utilruntime.ReallyCrash = previousReallyCrash }()
	_, err := fetchTagMetadataConcurrently(context.Background(), []Tag{{Name: "latest"}}, func(context.Context, Tag) (fetchedTagMetadata, error) {
		panic("metadata panic")
	})
	if err == nil || !strings.Contains(err.Error(), "panic while enriching tag latest") {
		t.Fatalf("fetchTagMetadataConcurrently() error = %v, want panic error", err)
	}
}
