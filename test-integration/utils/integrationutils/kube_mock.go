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
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/testing"

	"sigs.k8s.io/yaml"

	sessiongateapiv1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	sessiongatefake "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/clientset/versioned/fake"
	sessiongateinformers "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/informers/externalversions"
)

// fakeClient bundles a fake and its tracker for routing by API group.
type fakeClient struct {
	fake    *testing.Fake
	tracker testing.ObjectTracker
}

// KubernetesClientSets provides fake clientsets with informer support for testing.
//
// Uses the pattern from kubernetes/client-go/examples/fake-client to properly
// synchronize fake clients with informers. The key is waiting for the watcher
// to start before injecting events.
// See: https://github.com/kubernetes/client-go/blob/master/examples/fake-client/main_test.go
type KubernetesClientSets struct {
	FakeClientSet          *fake.Clientset
	SessiongateClientset   *sessiongatefake.Clientset
	CoreInformerFactory    kubeinformers.SharedInformerFactory
	SessionInformerFactory sessiongateinformers.SharedInformerFactory

	clients map[string]*fakeClient
	decoder runtime.Decoder
	stopCh  chan struct{}
}

func (k *KubernetesClientSets) Stop() {
	close(k.stopCh)
}

// NewKubernetesClientSets creates fake clientsets with informer support.
// Informers are started and synchronized before returning.
func NewKubernetesClientSets(sessionNamespace string) *KubernetesClientSets {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := sessiongateapiv1alpha1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	codecs := serializer.NewCodecFactory(scheme)
	decoder := codecs.UniversalDeserializer()

	// Standard kubernetes fake client
	fc := fake.NewSimpleClientset()

	// Sessiongate fake client
	sf := sessiongatefake.NewSimpleClientset()
	stopCh := make(chan struct{})

	// Build routing map by API group. To add a new client, add it here.
	clients := map[string]*fakeClient{
		sessiongateapiv1alpha1.SchemeGroupVersion.Group: {fake: &sf.Fake, tracker: sf.Tracker()},
		"": {fake: &fc.Fake, tracker: fc.Tracker()},
	}

	// WaitGroup to wait for all watchers to start (per client-go example pattern).
	var watchersReady sync.WaitGroup
	watchersReady.Add(len(clients))

	for _, client := range clients {
		// Per-client state for reactors
		var watcherOnce sync.Once
		var generateNameMu sync.Mutex
		generateNameCounter := 0

		// Capture client for use in closures
		c := client

		// Watch reactor to signal when watcher starts
		c.fake.PrependWatchReactor("*", func(action testing.Action) (bool, watch.Interface, error) {
			gvr := action.GetResource()
			ns := action.GetNamespace()
			var opts metav1.ListOptions
			if watchAction, ok := action.(testing.WatchActionImpl); ok {
				opts = watchAction.ListOptions
			}
			watcher, err := c.tracker.Watch(gvr, ns, opts)
			if err != nil {
				return false, nil, err
			}
			watcherOnce.Do(func() { watchersReady.Done() })
			return true, watcher, nil
		})

		// GenerateName reactor for all resource types (fake clients don't handle this by default)
		c.fake.PrependReactor("create", "*", func(action testing.Action) (bool, runtime.Object, error) {
			createAction := action.(testing.CreateAction)
			obj := createAction.GetObject()
			meta := obj.(metav1.Object)

			if meta.GetName() == "" && meta.GetGenerateName() != "" {
				generateNameMu.Lock()
				generateNameCounter++
				count := generateNameCounter
				generateNameMu.Unlock()
				meta.SetName(fmt.Sprintf("%s%d", meta.GetGenerateName(), count))
			}

			// Let default reactor chain handle the rest
			return false, nil, nil
		})
	}

	// Core kubernetes informer factory
	coreInformerFactory := kubeinformers.NewSharedInformerFactory(fc, 0)
	_ = coreInformerFactory.Core().V1().Secrets().Informer()

	// Sessiongate informer factory
	sessionInformerFactory := sessiongateinformers.NewSharedInformerFactoryWithOptions(sf, 0)
	_ = sessionInformerFactory.Sessiongate().V1alpha1().Sessions().Informer()

	// Start all informers and wait for sync
	coreInformerFactory.Start(stopCh)
	sessionInformerFactory.Start(stopCh)
	coreInformerFactory.WaitForCacheSync(stopCh)
	sessionInformerFactory.WaitForCacheSync(stopCh)
	watchersReady.Wait()

	return &KubernetesClientSets{
		FakeClientSet:          fc,
		SessiongateClientset:   sf,
		CoreInformerFactory:    coreInformerFactory,
		SessionInformerFactory: sessionInformerFactory,
		clients:                clients,
		decoder:                decoder,
		stopCh:                 stopCh,
	}
}

func (k *KubernetesClientSets) getClient(group string) *fakeClient {
	if client, ok := k.clients[group]; ok {
		return client
	}
	return k.clients[""]
}

func (k *KubernetesClientSets) Create(ctx context.Context, data []byte) error {
	obj, gvk, err := k.decoder.Decode(data, nil, nil)
	if err != nil {
		return err
	}

	meta := obj.(metav1.Object)
	gvr := gvrFromGVK(*gvk)
	client := k.getClient(gvk.Group)

	action := testing.NewCreateAction(gvr, meta.GetNamespace(), obj)
	_, err = client.fake.Invokes(action, obj)
	return err
}

func (k *KubernetesClientSets) Apply(ctx context.Context, data []byte) error {
	var rawObj map[string]interface{}
	if err := yaml.Unmarshal(data, &rawObj); err != nil {
		return err
	}

	obj := &unstructured.Unstructured{Object: rawObj}
	gvk := obj.GetObjectKind().GroupVersionKind()
	gvr := gvrFromGVK(gvk)
	client := k.getClient(gvk.Group)

	patchData, err := json.Marshal(rawObj)
	if err != nil {
		return err
	}

	action := testing.NewPatchAction(gvr, obj.GetNamespace(), obj.GetName(), types.ApplyPatchType, patchData)
	_, err = client.fake.Invokes(action, obj)
	return err
}

func gvrFromGVK(gvk schema.GroupVersionKind) schema.GroupVersionResource {
	gvr, _ := meta.UnsafeGuessKindToResource(gvk)
	return gvr
}

func (k *KubernetesClientSets) GetTrackedObject(ctx context.Context, expected *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	gvk := expected.GetObjectKind().GroupVersionKind()
	gvr := gvrFromGVK(gvk)
	client := k.getClient(gvk.Group)

	actualObj, err := client.tracker.Get(gvr, expected.GetNamespace(), expected.GetName())
	if err != nil {
		return nil, err
	}

	unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(actualObj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert object to unstructured: %w", err)
	}
	unstructuredActual := &unstructured.Unstructured{Object: unstructuredMap}
	unstructuredActual.SetGroupVersionKind(gvk)
	return unstructuredActual, nil
}
