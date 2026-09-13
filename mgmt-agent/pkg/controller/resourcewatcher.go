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

package controller

import (
	"context"
	"os"
	"strings"
	"sync"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

// watchedGroupSuffixes is the hardcoded list of API group domain suffixes
// whose resources will be discovered and watched.
var watchedGroupSuffixes = []string{
	"open-cluster-management.io",
	"cluster.x-k8s.io",
	"hypershift.openshift.io",
	"agent-install.openshift.io",
	"multicluster.openshift.io",
	"multitenancy.acn.azure.com",
	"velero.io",
	"route.openshift.io",
}

// watchedExplicitGVRs is the hardcoded list of GroupVersionResources to watch
// in addition to everything discovered via watchedGroupSuffixes. These APIs
// are not covered by the group suffixes above but still carry useful
// management-cluster state.
var watchedExplicitGVRs = []schema.GroupVersionResource{
	{Group: "", Version: "v1", Resource: "namespaces"},
	{Group: "", Version: "v1", Resource: "nodes"},
	{Group: "", Version: "v1", Resource: "configmaps"},
	{Group: "", Version: "v1", Resource: "endpoints"},
	{Group: "", Version: "v1", Resource: "persistentvolumeclaims"},
	{Group: "", Version: "v1", Resource: "services"},
	{Group: "apps", Version: "v1", Resource: "deployments"},
	{Group: "apps", Version: "v1", Resource: "daemonsets"},
	{Group: "apps", Version: "v1", Resource: "statefulsets"},
	{Group: "apps", Version: "v1", Resource: "replicasets"},
	{Group: "batch", Version: "v1", Resource: "cronjobs"},
	{Group: "batch", Version: "v1", Resource: "jobs"},
	{Group: "monitoring.coreos.com", Version: "v1", Resource: "podmonitors"},
	{Group: "monitoring.coreos.com", Version: "v1", Resource: "servicemonitors"},
	{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
	{Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"},
}

// ServerResourceDiscoverer is the subset of the discovery API that ResourceWatcher needs.
type ServerResourceDiscoverer interface {
	ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error)
}

// ResourceWatcher discovers API resources matching a set of group suffixes, also
// watches a fixed set of additional GroupVersionResources (watchedExplicitGVRs), and
// logs every event via reflectors as structured JSON.
type ResourceWatcher struct {
	dynamicClient   dynamic.Interface
	discoveryClient ServerResourceDiscoverer
}

// NewResourceWatcher creates a new ResourceWatcher.
func NewResourceWatcher(dynamicClient dynamic.Interface, discoveryClient ServerResourceDiscoverer) *ResourceWatcher {
	return &ResourceWatcher{
		dynamicClient:   dynamicClient,
		discoveryClient: discoveryClient,
	}
}

// Run discovers GVRs for the configured group suffixes, also watches the GVRs in
// watchedExplicitGVRs, starts reflectors for each, and blocks until
// the context is cancelled. Events are logged as structured JSON via klog. A
// CRD reflector watches for new CustomResourceDefinitions;
// if a new CRD is registered whose group matches the watched suffixes and introduces
// GVRs not known at startup, the process exits so the pod restarts and picks them up.
func (w *ResourceWatcher) Run(ctx context.Context) error {
	logger := klog.FromContext(ctx)
	logger.Info("Starting resource watcher")

	gvrs, err := w.discoverGVRs()
	if err != nil {
		return err
	}
	gvrs = append(gvrs, watchedExplicitGVRs...)
	logger.Info("Discovered resources to watch", "count", len(gvrs))

	knownGVRs := sets.New[schema.GroupVersionResource](gvrs...)

	// Watch CRDs so we can detect when new API resources matching our group
	// suffixes are registered in the cluster. When that happens, we exit the
	// process so the pod restarts and picks up the new GVRs.
	crdGVR := schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}
	handleCRD := func(obj interface{}) {
		newGVRs := newMatchingGVRsFromCRD(obj, knownGVRs)
		if newGVRs.Len() > 0 {
			logger.Error(nil, "New CRD introduces GVRs that should be watched, exiting to trigger pod restart",
				"newGVRs", newGVRs.UnsortedList())
			klog.Flush()
			os.Exit(1)
		}
	}
	var syncChannels []<-chan struct{}
	crdStore := newReflectorStore(
		handleCRD,
		handleCRD,
		func(interface{}) {},
	)
	syncChannels = append(syncChannels, crdStore.synced)
	go newDynamicReflector(w.dynamicClient, crdGVR, crdStore).RunWithContext(ctx)

	for _, gvr := range gvrs {
		gvr := gvr
		store := newReflectorStore(
			func(obj interface{}) { logResourceEvent(ctx, "Add", gvr, obj) },
			func(obj interface{}) { logResourceEvent(ctx, "Update", gvr, obj) },
			func(obj interface{}) { logResourceEvent(ctx, "Delete", gvr, obj) },
		)
		syncChannels = append(syncChannels, store.synced)
		go newDynamicReflector(w.dynamicClient, gvr, store).RunWithContext(ctx)
	}

	for _, synced := range syncChannels {
		select {
		case <-synced:
		case <-ctx.Done():
			logger.Info("Shutting down resource watcher")
			return nil
		}
	}

	logger.Info("Resource watcher reflectors synced and running")
	<-ctx.Done()
	logger.Info("Shutting down resource watcher")
	return nil
}

// reflectorStore implements cache.ReflectorStore without implementing a
// cache. This is deliberately unusual: an informer normally retains the last
// copy of every watched object so consumers can perform indexed reads and
// compare old and new values. The resource watcher has no such consumers; its
// only purpose is to emit every observed snapshot for later must-gather use.
// Retaining another full copy of all management-cluster resources would waste
// substantial memory, so each callback logs the object and immediately drops
// the reference.
type reflectorStore struct {
	add      func(interface{})
	update   func(interface{})
	delete   func(interface{})
	synced   chan struct{}
	syncOnce sync.Once
}

var _ cache.ReflectorStore = &reflectorStore{}

func newReflectorStore(add, update, delete func(interface{})) *reflectorStore {
	return &reflectorStore{
		add:    add,
		update: update,
		delete: delete,
		synced: make(chan struct{}),
	}
}

func (s *reflectorStore) Add(obj interface{}) error {
	s.add(obj)
	return nil
}

func (s *reflectorStore) Update(obj interface{}) error {
	s.update(obj)
	return nil
}

func (s *reflectorStore) Delete(obj interface{}) error {
	s.delete(obj)
	return nil
}

func (s *reflectorStore) Replace(objects []interface{}, _ string) error {
	// Reflector delivers the initial LIST through Replace rather than Add. Log
	// those objects as additions so startup still produces a complete snapshot,
	// then release the slice without retaining any of its contents.
	for _, obj := range objects {
		s.add(obj)
	}
	s.syncOnce.Do(func() { close(s.synced) })
	return nil
}

func (s *reflectorStore) Resync() error {
	// There is intentionally no retained state to replay.
	return nil
}

func newDynamicReflector(dynamicClient dynamic.Interface, gvr schema.GroupVersionResource, store cache.ReflectorStore) *cache.Reflector {
	resourceClient := dynamicClient.Resource(gvr)
	listWatch := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			return resourceClient.List(ctx, options)
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			return resourceClient.Watch(ctx, options)
		},
	}
	return cache.NewReflectorWithOptions(
		listWatch,
		&unstructured.Unstructured{},
		store,
		cache.ReflectorOptions{
			Name:            "resource-watcher/" + gvr.String(),
			TypeDescription: gvr.String(),
		},
	)
}

// discoverGVRs uses the discovery API to find all GVRs whose group matches
// one of the watched suffixes and that support both list and watch verbs.
func (w *ResourceWatcher) discoverGVRs() ([]schema.GroupVersionResource, error) {
	_, apiResourceLists, err := w.discoveryClient.ServerGroupsAndResources()
	if err != nil {
		// Partial discovery failure is acceptable — some groups may be
		// unavailable, but we can still watch what we found.
		if apiResourceLists == nil {
			return nil, err
		}
		klog.Warningf("Partial discovery failure (continuing with available groups): %v", err)
	}

	var gvrs []schema.GroupVersionResource
	for _, list := range apiResourceLists {
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil {
			continue
		}
		if !matchesGroupSuffix(gv.Group) {
			continue
		}
		for _, resource := range list.APIResources {
			if !supportsListWatch(resource) {
				continue
			}
			// Skip subresources (e.g. pods/status).
			if strings.Contains(resource.Name, "/") {
				continue
			}
			gvrs = append(gvrs, schema.GroupVersionResource{
				Group:    gv.Group,
				Version:  gv.Version,
				Resource: resource.Name,
			})
		}
	}
	return gvrs, nil
}

// matchesGroupSuffix returns true if the group equals or is a subdomain of
// one of the watched suffixes.
func matchesGroupSuffix(group string) bool {
	for _, suffix := range watchedGroupSuffixes {
		if group == suffix || strings.HasSuffix(group, "."+suffix) {
			return true
		}
	}
	return false
}

// supportsListWatch returns true if the API resource supports both list and watch verbs.
func supportsListWatch(r metav1.APIResource) bool {
	hasList := false
	hasWatch := false
	for _, v := range r.Verbs {
		switch v {
		case "list":
			hasList = true
		case "watch":
			hasWatch = true
		}
	}
	return hasList && hasWatch
}

// newMatchingGVRsFromCRD extracts GVRs from a CRD object and returns those
// that match the watched group suffixes but are not in the known set.
func newMatchingGVRsFromCRD(obj interface{}, knownGVRs sets.Set[schema.GroupVersionResource]) sets.Set[schema.GroupVersionResource] {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil
	}

	var crd apiextensionsv1.CustomResourceDefinition
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &crd); err != nil {
		return nil
	}

	if !matchesGroupSuffix(crd.Spec.Group) {
		return nil
	}

	crdGVRs := sets.New[schema.GroupVersionResource]()
	for _, version := range crd.Spec.Versions {
		if !version.Served {
			continue
		}
		crdGVRs.Insert(schema.GroupVersionResource{
			Group:    crd.Spec.Group,
			Version:  version.Name,
			Resource: crd.Spec.Names.Plural,
		})
	}

	return crdGVRs.Difference(knownGVRs)
}

// logResourceEvent logs a resource event with the object as a structured field.
func logResourceEvent(ctx context.Context, eventType string, gvr schema.GroupVersionResource, obj interface{}) {
	logger := klog.FromContext(ctx)
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		logger.Error(nil, "Unexpected object type in resource watcher", "event", eventType, "gvr", gvr.String())
		return
	}
	logger.Info("resource event",
		"snapshotType", "kubernetes",
		"event", eventType,
		"gvr", gvr.String(),
		"namespace", u.GetNamespace(),
		"name", u.GetName(),
		"object", u.Object,
	)
}
