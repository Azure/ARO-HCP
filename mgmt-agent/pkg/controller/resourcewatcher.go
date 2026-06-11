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
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
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
}

// ServerResourceDiscoverer is the subset of the discovery API that ResourceWatcher needs.
type ServerResourceDiscoverer interface {
	ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error)
}

// ResourceWatcher discovers API resources matching a set of group suffixes
// and watches them via dynamic informers, logging every event to stdout as
// structured JSON.
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

// Run discovers GVRs for the configured group suffixes, starts dynamic informers
// for each, and blocks until the context is cancelled. Events are logged as
// structured JSON via klog. A CRD informer watches for new CustomResourceDefinitions;
// if a new CRD is registered whose group matches the watched suffixes and introduces
// GVRs not known at startup, the process exits so the pod restarts and picks them up.
func (w *ResourceWatcher) Run(ctx context.Context) error {
	logger := klog.FromContext(ctx)
	logger.Info("Starting resource watcher")

	gvrs, err := w.discoverGVRs()
	if err != nil {
		return err
	}
	logger.Info("Discovered resources to watch", "count", len(gvrs))

	knownGVRs := sets.New[schema.GroupVersionResource](gvrs...)

	factory := dynamicinformer.NewDynamicSharedInformerFactory(w.dynamicClient, 10*time.Hour)

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
	crdInformer := factory.ForResource(crdGVR)
	if _, err := crdInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    handleCRD,
		UpdateFunc: func(_, obj interface{}) { handleCRD(obj) },
	}); err != nil {
		return err
	}

	for _, gvr := range gvrs {
		informer := factory.ForResource(gvr)
		gvr := gvr // capture for closures
		if _, err := informer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				logResourceEvent(ctx, "Add", gvr, obj)
			},
			UpdateFunc: func(_, obj interface{}) {
				logResourceEvent(ctx, "Update", gvr, obj)
			},
			DeleteFunc: func(obj interface{}) {
				if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
					obj = tombstone.Obj
				}
				logResourceEvent(ctx, "Delete", gvr, obj)
			},
		}); err != nil {
			return err
		}
	}

	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	logger.Info("Resource watcher informers synced and running")
	<-ctx.Done()
	logger.Info("Shutting down resource watcher")
	return nil
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
		"event", eventType,
		"gvr", gvr.String(),
		"namespace", u.GetNamespace(),
		"name", u.GetName(),
		"object", u.Object,
	)
}
