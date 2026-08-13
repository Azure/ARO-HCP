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

package fleetinformers

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/informerutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type recordingRolloutLister struct {
	calls chan context.Context
}

func (l recordingRolloutLister) List(ctx context.Context, _ *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[fleetapi.ControlPlaneVersionRollout], error) {
	select {
	case l.calls <- ctx:
	case <-ctx.Done():
	}
	return emptyRolloutIterator{}, nil
}

type emptyRolloutIterator struct{}

func (emptyRolloutIterator) Items(context.Context) cosmosstorageutils.DBClientIteratorItem[fleetapi.ControlPlaneVersionRollout] {
	return func(func(string, *fleetapi.ControlPlaneVersionRollout) bool) {}
}
func (emptyRolloutIterator) GetContinuationToken() string { return "" }
func (emptyRolloutIterator) GetError() error              { return nil }

// No feed ranges are needed to exercise the production informer's initial list.
type noRangesChangeFeedClient struct{}

func (noRangesChangeFeedClient) ReadFeedRanges(context.Context, *azcosmos.FeedRangesOptions) ([]azcosmos.FeedRange, error) {
	return nil, nil
}

func (noRangesChangeFeedClient) ReadChangeFeed(context.Context, *azcosmos.ChangeFeedOptions) (azcosmos.ChangeFeedResponse, error) {
	panic("unexpected change-feed read without feed ranges")
}

func TestControlPlaneVersionRolloutInformerAttribution(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = utils.ContextWithLogger(ctx, logr.Discard())
	ctx = utils.ContextWithControllerName(ctx, "ConsumingController")

	calls := make(chan context.Context, 1)
	informer := NewControlPlaneVersionRolloutInformer(recordingRolloutLister{calls: calls}, noRangesChangeFeedClient{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		informer.RunWithContext(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("informer did not stop after cancellation")
		}
	})

	select {
	case listCtx := <-calls:
		name, ok := informerutils.InformerNameFromContext(listCtx)
		require.True(t, ok, "list lacks informer attribution")
		require.Equal(t, "ControlPlaneVersionRollouts", name)
		controller, ok := utils.ControllerNameFromContext(listCtx)
		require.True(t, ok, "list discarded parent controller attribution")
		require.Equal(t, "ConsumingController", controller)
	case <-ctx.Done():
		t.Fatal("timed out waiting for the informer's first list call")
	}
}
