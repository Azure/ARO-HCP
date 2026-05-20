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

package controllerutils

import (
	"context"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
)

type SubscriptionSyncer interface {
	SyncOnce(ctx context.Context, keyObj SubscriptionKey) error
	CooldownChecker() controllerutil.CooldownChecker
}

type subscriptionWatchingController struct {
	name   string
	syncer SubscriptionSyncer
}

// NewSubscriptionWatchingController periodically looks up all subscriptions and queues them.
// cooldownDuration is how long to wait before allowing a new notification to fire the controller.
// Since our detection of change is coarse, we are being triggered every few second without new information.
// Until we get a changefeed, the cooldownDuration value is effectively the min resync time.
// This does NOT prevent us from re-executing on errors, so errors will continue to trigger fast checks as expected.
func NewSubscriptionWatchingController(
	name string,
	informers informers.BackendInformers,
	resyncDuration time.Duration,
	syncer SubscriptionSyncer,
) Controller {
	subscriptionSyncer := &subscriptionWatchingController{
		name:   name,
		syncer: syncer,
	}
	subscriptionController := newGenericWatchingController(name, azcorearm.SubscriptionResourceType, subscriptionSyncer)

	// this happens when unit tests don't want triggering.  This isn't beautiful, but fails to do nothing which is pretty safe.
	if informers != nil {
		subscriptionInformer, _ := informers.Subscriptions()
		err := subscriptionController.QueueForInformers(resyncDuration, subscriptionInformer)
		if err != nil {
			panic(err) // coding error
		}
	}

	return subscriptionController
}

func (c *subscriptionWatchingController) SyncOnce(ctx context.Context, key SubscriptionKey) error {
	return c.syncer.SyncOnce(ctx, key)
}

func (c *subscriptionWatchingController) CooldownChecker() controllerutil.CooldownChecker {
	return c.syncer.CooldownChecker()
}

func (c *subscriptionWatchingController) MakeKey(resourceID *azcorearm.ResourceID) SubscriptionKey {
	return SubscriptionKey{
		SubscriptionID: resourceID.SubscriptionID,
	}
}
