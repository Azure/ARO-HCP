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

package kubeapplier_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/database/informers/kubeapplierinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplier "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
)

type readinessMCInformer struct {
	*fakeMCInformer
	cacheReady  atomic.Bool
	eventsReady atomic.Bool
}

type readinessRegistration struct {
	cache.ResourceEventHandlerRegistration
	ready *atomic.Bool
}

func (registration *readinessRegistration) HasSynced() bool { return registration.ready.Load() }

func (informer *readinessMCInformer) HasSynced() bool { return informer.cacheReady.Load() }

func (informer *readinessMCInformer) AddEventHandler(handler cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
	registration, err := informer.fakeMCInformer.AddEventHandler(handler)
	return &readinessRegistration{ResourceEventHandlerRegistration: registration, ready: &informer.eventsReady}, err
}

func (informer *readinessMCInformer) RemoveEventHandler(registration cache.ResourceEventHandlerRegistration) error {
	return informer.fakeMCInformer.RemoveEventHandler(registration.(*readinessRegistration).ResourceEventHandlerRegistration)
}

type readinessInformer struct {
	cache.SharedIndexInformer
	ready atomic.Bool
}

func (informer *readinessInformer) HasSynced() bool { return informer.ready.Load() }

type readinessSubInformers struct {
	read    *readinessInformer
	apply   *readinessInformer
	started chan struct{}
}

func newReadinessSubInformers() *readinessSubInformers {
	newInformer := func() *readinessInformer {
		return &readinessInformer{SharedIndexInformer: cache.NewSharedIndexInformer(&cache.ListWatch{}, &metav1.PartialObjectMetadata{}, 0, cache.Indexers{})}
	}
	return &readinessSubInformers{read: newInformer(), apply: newInformer(), started: make(chan struct{})}
}

func (informers *readinessSubInformers) ReadDesires() (cache.SharedIndexInformer, kubeapplierlisters.ReadDesireLister) {
	return informers.read, kubeapplierlisters.NewReadDesireLister(informers.read.GetIndexer())
}

func (informers *readinessSubInformers) ApplyDesires() (cache.SharedIndexInformer, kubeapplierlisters.ApplyDesireLister) {
	return informers.apply, kubeapplierlisters.NewApplyDesireLister(informers.apply.GetIndexer())
}

func (informers *readinessSubInformers) RunWithContext(ctx context.Context) {
	close(informers.started)
	<-ctx.Done()
}

type readinessFactory struct {
	subs      map[string]*readinessSubInformers
	available atomic.Bool
	attempts  atomic.Int32
}

func (factory *readinessFactory) NewKubeApplierInformers(_ context.Context, resourceID *azcorearm.ResourceID) kubeapplierinformers.KubeApplierInformers {
	factory.attempts.Add(1)
	if !factory.available.Load() {
		return nil
	}
	return factory.subs[strings.ToLower(resourceID.String())]
}

func TestUnionReadinessWaitsForDiscoveryAndEveryExpectedIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	managementInformer := &readinessMCInformer{fakeMCInformer: newFakeMCInformer()}
	lister := &mutableMCLister{}
	lister.set(ctlMC(ctlMgmtAID), ctlMC(ctlMgmtBID))
	subA, subB := newReadinessSubInformers(), newReadinessSubInformers()
	factory := &readinessFactory{subs: map[string]*readinessSubInformers{
		strings.ToLower(ctlMgmtAID.String()): subA,
		strings.ToLower(ctlMgmtBID.String()): subB,
	}}
	controller := unionkubeapplier.NewUnionKubeApplierInformersController(managementInformer, lister, factory)
	consumerDone := make(chan bool, 1)
	go func() { consumerDone <- cache.WaitForCacheSync(ctx.Done(), controller.Union().HasSynced) }()
	require.False(t, controller.Union().HasSynced())
	done := make(chan struct{})
	go func() { defer close(done); controller.Run(ctx, 1) }()
	require.Eventually(t, func() bool { return managementInformer.handlerCount() == 1 }, 5*time.Second, time.Millisecond)
	managementInformer.emitAdd(ctlMC(ctlMgmtAID))
	managementInformer.emitAdd(ctlMC(ctlMgmtBID))
	managementInformer.cacheReady.Store(true)
	require.Never(t, controller.Union().HasSynced, 120*time.Millisecond, time.Millisecond)
	require.Zero(t, factory.attempts.Load(), "workers must wait for initial handler delivery")
	managementInformer.eventsReady.Store(true)
	require.Eventually(t, func() bool { return factory.attempts.Load() >= 2 }, 5*time.Second, time.Millisecond)
	require.False(t, controller.Union().HasSynced(), "nil factories cannot count as initialized")
	factory.available.Store(true)
	for _, sub := range []*readinessSubInformers{subA, subB} {
		select {
		case <-sub.started:
		case <-time.After(5 * time.Second):
			t.Fatal("nil factory was not retried without another MC event")
		}
	}
	subA.read.ready.Store(true)
	subA.apply.ready.Store(true)
	subB.read.ready.Store(true)
	require.Never(t, controller.Union().HasSynced, 120*time.Millisecond, time.Millisecond)
	require.Empty(t, consumerDone, "one MC's unsynced ApplyDesire cache must block consumers")
	subB.apply.ready.Store(true)
	select {
	case synced := <-consumerDone:
		require.True(t, synced)
	case <-time.After(5 * time.Second):
		t.Fatal("consumer did not observe authoritative union readiness")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("controller did not stop")
	}
	require.Zero(t, managementInformer.handlerCount())
}

func TestUnionReadinessEmptyFleetAndCancellation(t *testing.T) {
	for _, cancelBeforeDiscovery := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty-fleet", true: "cancel-before-discovery"}[cancelBeforeDiscovery], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			informer := &readinessMCInformer{fakeMCInformer: newFakeMCInformer()}
			controller := unionkubeapplier.NewUnionKubeApplierInformersController(informer, &mutableMCLister{}, &readinessFactory{})
			done := make(chan struct{})
			go func() { defer close(done); controller.Run(ctx, 1) }()
			require.Eventually(t, func() bool { return informer.handlerCount() == 1 }, 5*time.Second, time.Millisecond)
			require.False(t, controller.Union().HasSynced(), "empty is not ready before discovery")
			if !cancelBeforeDiscovery {
				informer.cacheReady.Store(true)
				informer.eventsReady.Store(true)
				require.Eventually(t, controller.Union().HasSynced, 5*time.Second, time.Millisecond)
			}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("discovery wait ignored cancellation")
			}
			require.Zero(t, informer.handlerCount())
			if cancelBeforeDiscovery {
				require.False(t, controller.Union().HasSynced())
			}
		})
	}
}
