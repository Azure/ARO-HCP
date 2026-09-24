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

package repositorycleanup

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/repositorycleanup/storage"
)

const relationIndex = "repository-cleanup-relations"

type key struct {
	Resource schema.GroupVersionResource
	Name     string
	Relation string
}

func (k key) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues("resource", k.Resource.Resource, "name", k.Name, "relation", k.Relation)
}

type Controller struct {
	name         string
	client       dynamic.Interface
	deletePrefix func(context.Context, storage.Target) error
	informers    map[schema.GroupVersionResource]cache.SharedIndexInformer
	hasSynced    []cache.InformerSynced
	queue        workqueue.TypedRateLimitingInterface[key]
}

// NewController registers routing-only event handlers. Caches determine which
// keys to enqueue, never whether any resource or external data may be deleted.
func NewController(dynamicClient dynamic.Interface, deletePrefix func(context.Context, storage.Target) error, informers map[schema.GroupVersionResource]cache.SharedIndexInformer) (*Controller, error) {
	if dynamicClient == nil || deletePrefix == nil {
		return nil, fmt.Errorf("dynamic client and prefix deleter are required")
	}
	for _, gvr := range RequiredResources() {
		if informers[gvr] == nil {
			return nil, fmt.Errorf("missing informer for %s", gvr)
		}
	}
	c := &Controller{name: RepositoryCleanupControllerName, client: dynamicClient, deletePrefix: deletePrefix,
		informers: make(map[schema.GroupVersionResource]cache.SharedIndexInformer),
		queue:     workqueue.NewTypedRateLimitingQueueWithConfig(workqueue.DefaultTypedControllerRateLimiter[key](), workqueue.TypedRateLimitingQueueConfig[key]{Name: RepositoryCleanupControllerName})}
	for _, gvr := range RequiredResources() {
		informer := informers[gvr]
		c.informers[gvr] = informer
		c.hasSynced = append(c.hasSynced, informer.HasSynced)
		if gvr == BackupRepositoriesGVR || gvr == BackupRepositoryCleanupsGVR {
			if err := informer.AddIndexers(cache.Indexers{relationIndex: targetRelations}); err != nil {
				c.queue.ShutDown()
				return nil, err
			}
		}
		if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    func(obj interface{}) { c.enqueue(gvr, obj) },
			UpdateFunc: func(old, current interface{}) { c.enqueue(gvr, old); c.enqueue(gvr, current) },
			DeleteFunc: func(obj interface{}) { c.enqueue(gvr, obj) },
		}); err != nil {
			c.queue.ShutDown()
			return nil, err
		}
	}
	return c, nil
}

func targetRelations(obj interface{}) ([]string, error) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("repository and cleanup informers must be unstructured")
	}
	if u.GetNamespace() != Namespace {
		return nil, nil
	}
	volume, name := field(u, "spec", "volumeNamespace"), u.GetName()
	if u.GetKind() == "BackupRepositoryCleanup" {
		volume, name = field(u, "spec", "repository", "volumeNamespace"), field(u, "spec", "repository", "name")
	}
	return []string{"all", "volume/" + volume, "hc/" + HostedClusterNamespace(volume), "repo/" + name,
		"maintenance/" + RepositoryLabel(name)}, nil
}

func (c *Controller) enqueue(gvr schema.GroupVersionResource, obj interface{}) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	m, err := meta.Accessor(obj)
	if err != nil {
		return
	}
	if gvr == HostedClustersGVR {
		// Always enqueue the routing key, even for terminating HCs and updates
		// with identical resource versions. Fanout happens at dequeue.
		c.queue.Add(key{Relation: "hc/" + m.GetNamespace()})
		return
	}
	if m.GetNamespace() != Namespace {
		return
	}
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}
	route := func(relation string) { c.queue.Add(key{Relation: relation}) }
	switch gvr {
	case BackupRepositoriesGVR, BackupRepositoryCleanupsGVR:
		c.queue.Add(key{Resource: gvr, Name: u.GetName()})
		volume, name := field(u, "spec", "volumeNamespace"), u.GetName()
		if gvr == BackupRepositoryCleanupsGVR {
			volume, name = field(u, "spec", "repository", "volumeNamespace"), field(u, "spec", "repository", "name")
		}
		route("repo/" + name)
		route("maintenance/" + RepositoryLabel(name))
		route("volume/" + volume)
	case JobsGVR, PodsGVR:
		if label := u.GetLabels()[repoNameLabel]; label != "" {
			route("maintenance/" + label)
		}
		path := []string{"spec", "containers"}
		if gvr == JobsGVR {
			path = []string{"spec", "template", "spec", "containers"}
		}
		containers, _, _ := unstructured.NestedSlice(u.Object, path...)
		for _, raw := range containers {
			container, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			args, _, _ := unstructured.NestedStringSlice(container, "args")
			for _, arg := range args {
				if volume, ok := strings.CutPrefix(arg, "--repo-name="); ok {
					route("volume/" + volume)
				}
			}
		}
		// Resolve pod owner/job labels on dequeue; orphan pods retain the repo
		// label. Unlabeled unrelated jobs/pods never cause a fleet-wide rescan.
		if gvr == PodsGVR {
			for _, owner := range u.GetOwnerReferences() {
				if owner.Kind == "Job" {
					route("job/" + owner.Name)
				}
			}
			for _, label := range []string{"job-name", "batch.kubernetes.io/job-name"} {
				if name := u.GetLabels()[label]; name != "" {
					route("job/" + name)
				}
			}
		}
	case BackupStorageLocationsGVR:
		// A BSL can change physical aliases across names. Intents intentionally
		// do not refer back to mutable BSLs, so BSL events must fan out broadly.
		route("all")
	case BackupsGVR, SchedulesGVR:
		path := []string{"spec", "includedNamespaces"}
		if gvr == SchedulesGVR {
			path = []string{"spec", "template", "includedNamespaces"}
		}
		namespaces, _, err := unstructured.NestedStringSlice(u.Object, path...)
		if err != nil || len(namespaces) == 0 {
			route("all")
			return
		}
		for _, namespace := range namespaces {
			if namespace == "*" || namespace == "" {
				route("all")
			} else {
				route("volume/" + namespace)
			}
		}
	case RestoresGVR, PodVolumeRestoresGVR:
		route("all")
	default:
		volume := field(u, "spec", "sourceNamespace")
		if volume == "" {
			volume = field(u, "spec", "pod", "namespace")
		}
		if volume == "" {
			route("all")
		} else {
			route("volume/" + volume)
		}
	}
}

func (c *Controller) fanout(relation string) error {
	if name, ok := strings.CutPrefix(relation, "job/"); ok {
		obj, exists, err := c.informers[JobsGVR].GetIndexer().GetByKey(Namespace + "/" + name)
		if err != nil || !exists {
			return err
		}
		if job, ok := obj.(*unstructured.Unstructured); ok && job.GetLabels()[repoNameLabel] != "" {
			c.queue.Add(key{Relation: "maintenance/" + job.GetLabels()[repoNameLabel]})
		}
		return nil
	}
	for _, gvr := range []schema.GroupVersionResource{BackupRepositoriesGVR, BackupRepositoryCleanupsGVR} {
		objects, err := c.informers[gvr].GetIndexer().ByIndex(relationIndex, relation)
		if err != nil {
			return err
		}
		for _, obj := range objects {
			m, err := meta.Accessor(obj)
			if err != nil {
				return err
			}
			c.queue.Add(key{Resource: gvr, Name: m.GetName()})
		}
	}
	return nil
}

func (c *Controller) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()
	if workers < 1 {
		return fmt.Errorf("workers must be positive")
	}
	ctx = utils.ContextWithControllerName(ctx, c.name)
	ctx = utils.ContextWithLogger(ctx, utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(c.name)...))
	if !cache.WaitForCacheSync(ctx.Done(), c.hasSynced...) {
		return fmt.Errorf("failed to sync repository cleanup informers")
	}
	c.queue.Add(key{Relation: "all"})
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer utilruntime.HandleCrash()
			defer wg.Done()
			for c.processNext(ctx) {
			}
		}()
	}
	<-ctx.Done()
	c.queue.ShutDown()
	wg.Wait()
	return nil
}

func (c *Controller) processNext(ctx context.Context) bool {
	k, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(k)
	ctx = utils.ContextWithLogger(ctx, utils.AddLoggerValues(utils.LoggerFromContext(ctx), k))
	var delay time.Duration
	var err error
	if k.Relation != "" {
		err = c.fanout(k.Relation)
	} else {
		delay, err = c.reconcile(ctx, k)
	}
	if err != nil {
		utils.LoggerFromContext(ctx).Error(err, "Repository cleanup failed; retrying")
		c.queue.AddRateLimited(k)
	} else {
		c.queue.Forget(k)
		if delay > 0 {
			c.queue.AddAfter(k, delay)
		}
	}
	return true
}

func (c *Controller) get(ctx context.Context, gvr schema.GroupVersionResource, name string) (*unstructured.Unstructured, error) {
	obj, err := c.client.Resource(gvr).Namespace(Namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return obj, err
}

func (c *Controller) observe(ctx context.Context, k key) (Observations, error) {
	o := Observations{Objects: make(map[schema.GroupVersionResource][]unstructured.Unstructured), Now: time.Now()}
	obj, err := c.get(ctx, k.Resource, k.Name)
	if err != nil || obj == nil {
		return o, err
	}
	volume := ""
	if k.Resource == BackupRepositoriesGVR {
		o.Repository = obj
		volume = field(obj, "spec", "volumeNamespace")
		if obj.GetUID() != "" {
			o.Cleanup, err = c.get(ctx, BackupRepositoryCleanupsGVR, CleanupName(obj.GetUID()))
		}
	} else {
		o.Cleanup = obj
		volume = field(obj, "spec", "repository", "volumeNamespace")
		if name := field(obj, "spec", "repository", "name"); name != "" {
			o.Repository, err = c.get(ctx, BackupRepositoriesGVR, name)
		}
	}
	if err != nil {
		return o, err
	}
	// Resolve only this repository's BSL before the pure planner decides whether
	// broad dependency reads are needed. No cache miss authorizes an action.
	if k.Resource == BackupRepositoriesGVR && !preserved(o.Repository) && !preserved(o.Cleanup) {
		if name := field(o.Repository, "spec", "backupStorageLocation"); name != "" {
			bsl, err := c.get(ctx, BackupStorageLocationsGVR, name)
			if err != nil {
				return o, err
			}
			if bsl != nil {
				o.Objects[BackupStorageLocationsGVR] = []unstructured.Unstructured{*bsl}
			}
		}
	}
	if !plan(k, o).ObserveDependencies {
		return o, nil
	}
	if namespace := HostedClusterNamespace(volume); namespace != "" {
		list, err := c.client.Resource(HostedClustersGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return o, fmt.Errorf("list HostedClusters: %w", err)
		}
		if list.GetContinue() != "" {
			return o, fmt.Errorf("incomplete HostedCluster list")
		}
		o.HostedClusters = list.Items
	} else {
		return o, fmt.Errorf("cannot observe HostedClusters for unrecognized volume namespace")
	}
	if !plan(k, o).ObserveDependencies {
		return o, nil
	}
	for _, gvr := range RequiredResources() {
		if gvr == BackupRepositoryCleanupsGVR || gvr == HostedClustersGVR {
			continue
		}
		list, err := c.client.Resource(gvr).Namespace(Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return o, fmt.Errorf("list %s: %w", gvr, err)
		}
		if list.GetContinue() != "" {
			return o, fmt.Errorf("incomplete list of %s", gvr)
		}
		o.Objects[gvr] = list.Items
	}
	o.DependenciesComplete = true
	return o, nil
}

func plan(k key, o Observations) Plan {
	if k.Resource == BackupRepositoriesGVR {
		return PlanRepository(o)
	}
	return PlanCleanup(o)
}

func (c *Controller) apply(ctx context.Context, a Apply) error {
	// A lifecycle payload includes UID/RV when updating an existing object.
	// Only the immutable intent's initial apply intentionally has neither.
	data, err := json.Marshal(a.Object.Object)
	if err != nil {
		return err
	}
	var subresources []string
	if a.Subresource != "" {
		subresources = []string{a.Subresource}
	}
	_, err = c.client.Resource(a.Resource).Namespace(Namespace).Patch(ctx, a.Object.GetName(), types.ApplyPatchType, data, metav1.PatchOptions{FieldManager: a.FieldManager}, subresources...)
	return err
}

func lifecycle(p Plan) Plan {
	result := Plan{Deletes: p.Deletes, DeletePrefix: p.DeletePrefix}
	for _, a := range p.Applies {
		if a.Subresource == "" {
			result.Applies = append(result.Applies, a)
		}
	}
	return result
}

func (c *Controller) reconcile(ctx context.Context, k key) (time.Duration, error) {
	o, err := c.observe(ctx, k)
	if err != nil {
		return 0, err
	}
	p := plan(k, o)
	if k.Resource == BackupRepositoriesGVR && p.Condition != nil && p.Condition.Status == metav1.ConditionFalse {
		utils.LoggerFromContext(ctx).Info("Repository retirement blocked", "repository", k.Name, "reason", p.Condition.Reason, "message", p.Condition.Message)
	}
	for _, a := range p.Applies {
		if a.Subresource == "status" {
			if err := c.apply(ctx, a); err != nil {
				return 0, err
			}
		}
	}
	action := lifecycle(p)
	if len(action.Applies) == 0 && len(action.Deletes) == 0 && action.DeletePrefix == nil {
		return p.RequeueAfter, nil
	}
	// No mutation is authorized by cached event payloads, by an earlier sweep,
	// or by stale plans. Refetch everything after any preceding status apply.
	fresh, err := c.observe(ctx, k)
	if err != nil {
		return 0, err
	}
	confirmed := lifecycle(plan(k, fresh))
	if !reflect.DeepEqual(action, confirmed) {
		// A status write changes intent RV. Replan again on the next dequeue;
		// never silently substitute a different destructive action.
		return time.Second, nil
	}
	for _, a := range confirmed.Applies {
		if err := c.apply(ctx, a); err != nil {
			return 0, err
		}
	}
	for _, d := range confirmed.Deletes {
		if d.UID == "" || d.ResourceVersion == "" {
			return 0, fmt.Errorf("refusing deletion without UID/RV")
		}
		propagation := metav1.DeletePropagationBackground
		err := c.client.Resource(d.Resource).Namespace(Namespace).Delete(ctx, d.Name, metav1.DeleteOptions{
			Preconditions: &metav1.Preconditions{UID: &d.UID, ResourceVersion: &d.ResourceVersion}, PropagationPolicy: &propagation})
		if err != nil && !apierrors.IsNotFound(err) {
			return 0, err
		}
	}
	if confirmed.DeletePrefix != nil {
		if err := c.deletePrefix(ctx, *confirmed.DeletePrefix); err != nil {
			failed := blockedPlan(fresh, SweepFailed, "Prefix sweep failed; retrying")
			for _, a := range failed.Applies {
				if statusErr := c.apply(ctx, a); statusErr != nil {
					return 0, fmt.Errorf("sweep failed: %w; reporting failure: %v", err, statusErr)
				}
			}
			return 0, err
		}
		// The receipt is never persisted. Recheck all dependencies after the
		// sweep before reporting success or allowing explicit intent deletion.
		after, err := c.observe(ctx, k)
		if err != nil {
			return 0, err
		}
		if after.Cleanup == nil || fresh.Cleanup == nil || after.Cleanup.GetUID() != fresh.Cleanup.GetUID() ||
			!reflect.DeepEqual(after.Cleanup.Object["spec"], fresh.Cleanup.Object["spec"]) {
			return time.Second, nil
		}
		after.Swept = confirmed.DeletePrefix
		result := PlanCleanup(after)
		for _, a := range result.Applies {
			// Only status or finalizer release from a freshly verified successful
			// plan is legal here; other lifecycle steps require another reconcile.
			if a.Subresource == "status" || (result.Condition != nil && result.Condition.Status == metav1.ConditionTrue) {
				if err := c.apply(ctx, a); err != nil {
					return 0, err
				}
			}
		}
		return result.RequeueAfter, nil
	}
	return time.Second, nil
}
