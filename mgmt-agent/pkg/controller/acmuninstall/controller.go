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

// Package acmuninstall removes MultiClusterEngine / ACM leftovers from a
// management cluster in a fixed order and logs when each step finishes.
//
// After this has been merged and deployed to all production management
// clusters, remove "open-cluster-management.io" from watchedGroupSuffixes in
// the resource watcher and remove this controller.
package acmuninstall

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	ACMUninstallControllerName = "acm-uninstall"

	mceName      = "multiclusterengine"
	mceNamespace = "multicluster-engine"

	ocmCRDSuffix = ".open-cluster-management.io"

	// recheckInterval is how long to wait while a step is still draining.
	recheckInterval = 30 * time.Second
	// idleRecheckInterval is how long to wait after a successful uninstall
	// before verifying the cluster is still clean.
	idleRecheckInterval = 15 * time.Minute
)

var (
	MultiClusterEngineGVR = schema.GroupVersionResource{
		Group: "multicluster.openshift.io", Version: "v1", Resource: "multiclusterengines",
	}
	AppliedManifestWorksGVR = schema.GroupVersionResource{
		Group: "work.open-cluster-management.io", Version: "v1", Resource: "appliedmanifestworks",
	}
	CustomResourceDefinitionsGVR = schema.GroupVersionResource{
		Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions",
	}
)

const (
	stepEnsurePodNetworksNotACMOwned            = "ensure-podnetworks-not-acm-owned"
	stepEnsurePodNetworkInstancesNotACMOwned    = "ensure-podnetworkinstances-not-acm-owned"
	stepEnsureHostedClusterResourcesNotACMOwned = "ensure-hostedcluster-resources-not-acm-owned"
	stepDeleteMCE                               = "delete-multiclusterengine"
	stepDeleteNamespace                         = "delete-namespace"
	stepDeleteAppliedManifestWorks              = "delete-appliedmanifestworks"
	stepDeleteOCMCRDs                           = "delete-open-cluster-management-crds"
)

// syncKey is the single work item that drives the ordered uninstall.
type syncKey struct{}

func (syncKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues("sync", ACMUninstallControllerName)
}

// stepResult is the outcome of one uninstall step.
type stepResult struct {
	done   bool
	reason string
	values []any
}

// stepFunc runs one uninstall action or prerequisite check.
type stepFunc func(context.Context) (stepResult, error)

// step is one named action in an ordered sequence.
type step struct {
	name string
	run  stepFunc
}

// Controller tears down ACM/MCE leftovers after ownership prerequisites pass.
//
// Prerequisites (block teardown while any fail; checked until they succeed once):
//  1. PodNetwork objects are not ACM-owned
//  2. PodNetworkInstance objects are not ACM-owned
//  3. HostedClusters, their namespaced resources, and HostedControlPlane
//     namespaces are not ACM-owned
//
// After the prerequisites succeed once they are not rechecked: new clusters are
// no longer ACM-managed, so ownership cannot reappear for this purpose.
//
// Uninstall order once prerequisites pass:
//  1. MultiClusterEngine/multiclusterengine
//  2. Namespace/multicluster-engine
//  3. All AppliedManifestWorks (clear finalizers, then delete)
//  4. All CRDs whose names end in .open-cluster-management.io
type Controller struct {
	name             string
	kubeClient       kubernetes.Interface
	dynamicClient    dynamic.Interface
	queue            workqueue.TypedRateLimitingInterface[syncKey]
	prereqs          []step
	steps            []step
	prereqsSatisfied bool
}

func NewController(kubeClient kubernetes.Interface, dynamicClient dynamic.Interface) (*Controller, error) {
	if kubeClient == nil || dynamicClient == nil {
		return nil, fmt.Errorf("kube and dynamic clients are required")
	}
	c := &Controller{
		name:          ACMUninstallControllerName,
		kubeClient:    kubeClient,
		dynamicClient: dynamicClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[syncKey](),
			workqueue.TypedRateLimitingQueueConfig[syncKey]{Name: ACMUninstallControllerName},
		),
	}
	c.prereqs = []step{
		{name: stepEnsurePodNetworksNotACMOwned, run: c.ensurePodNetworksNotACMOwned},
		{name: stepEnsurePodNetworkInstancesNotACMOwned, run: c.ensurePodNetworkInstancesNotACMOwned},
		{name: stepEnsureHostedClusterResourcesNotACMOwned, run: c.ensureHostedClusterResourcesNotACMOwned},
	}
	c.steps = []step{
		{name: stepDeleteMCE, run: c.ensureMCEDeleted},
		{name: stepDeleteNamespace, run: c.ensureNamespaceDeleted},
		{name: stepDeleteAppliedManifestWorks, run: c.ensureAppliedManifestWorksDeleted},
		{name: stepDeleteOCMCRDs, run: c.ensureOCMCRDsDeleted},
	}
	return c, nil
}

func (c *Controller) Run(ctx context.Context) error {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting ACM uninstall controller")

	c.queue.Add(syncKey{})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		for c.processNext(ctx) {
		}
	}()

	<-ctx.Done()
	c.queue.ShutDown()
	wg.Wait()
	logger.Info("Stopped ACM uninstall controller")
	return nil
}

func (c *Controller) processNext(ctx context.Context) bool {
	key, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(key)

	logger := utils.AddLoggerValues(utils.LoggerFromContext(ctx), key)
	ctx = utils.ContextWithLogger(ctx, logger)

	requeueAfter, err := c.reconcile(ctx)
	if err != nil {
		logger.Error(err, "ACM uninstall reconcile failed; retrying")
		c.queue.AddRateLimited(key)
		return true
	}
	c.queue.Forget(key)
	if requeueAfter > 0 {
		c.queue.AddAfter(key, requeueAfter)
	}
	return true
}

// reconcile checks uninstall prerequisites (until they succeed once), then
// runs uninstall steps in order. It returns how long to wait before checking
// again. After a successful uninstall it rechecks on idleRecheckInterval in
// case ACM leftovers reappear.
func (c *Controller) reconcile(ctx context.Context) (time.Duration, error) {
	logger := utils.LoggerFromContext(ctx)

	if !c.prereqsSatisfied {
		for _, step := range c.prereqs {
			result, err := step.run(ctx)
			if err != nil {
				return 0, fmt.Errorf("prerequisite %q: %w", step.name, err)
			}
			if result.done {
				logger.Info("ACM uninstall prerequisite met", "step", step.name)
				continue
			}
			pendingLogger := logger.WithValues("step", step.name, "reason", result.reason)
			pendingLogger.Info("ACM uninstall prerequisite not met", result.values...)
			return recheckInterval, nil
		}
		c.prereqsSatisfied = true
		logger.Info("ACM uninstall prerequisites satisfied; skipping future ownership checks")
	}

	for _, step := range c.steps {
		result, err := step.run(ctx)
		if err != nil {
			return 0, fmt.Errorf("step %q: %w", step.name, err)
		}
		if result.done {
			logger.Info("ACM uninstall step completed", "step", step.name)
			continue
		}
		pendingLogger := logger.WithValues("step", step.name, "reason", result.reason)
		pendingLogger.Info("ACM uninstall step not done", result.values...)
		return recheckInterval, nil
	}

	logger.Info("ACM uninstall completed; all steps finished")
	return idleRecheckInterval, nil
}

func (c *Controller) ensureMCEDeleted(ctx context.Context) (stepResult, error) {
	logger := utils.LoggerFromContext(ctx)

	mce, err := c.dynamicClient.Resource(MultiClusterEngineGVR).Get(ctx, mceName, metav1.GetOptions{})
	if apiGone(err) {
		return stepResult{done: true}, nil
	}
	if err != nil {
		return stepResult{}, fmt.Errorf("get MultiClusterEngine %q: %w", mceName, err)
	}

	if mce.GetDeletionTimestamp().IsZero() {
		logger.Info("deleting MultiClusterEngine", "name", mceName)
		err := c.dynamicClient.Resource(MultiClusterEngineGVR).Delete(ctx, mceName, metav1.DeleteOptions{})
		if apiGone(err) {
			return stepResult{done: true}, nil
		}
		if err != nil {
			return stepResult{}, fmt.Errorf("delete MultiClusterEngine %q: %w", mceName, err)
		}
		return stepResult{done: false, reason: "delete issued; waiting for MultiClusterEngine to be removed", values: []any{"name", mceName}}, nil
	}

	return stepResult{done: false, reason: "MultiClusterEngine is terminating", values: []any{"mce", mce}}, nil
}

func (c *Controller) ensureNamespaceDeleted(ctx context.Context) (stepResult, error) {
	logger := utils.LoggerFromContext(ctx)

	ns, err := c.kubeClient.CoreV1().Namespaces().Get(ctx, mceNamespace, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return stepResult{done: true}, nil
	}
	if err != nil {
		return stepResult{}, fmt.Errorf("get namespace %q: %w", mceNamespace, err)
	}

	if ns.DeletionTimestamp.IsZero() {
		logger.Info("deleting ACM namespace", "namespace", mceNamespace)
		err := c.kubeClient.CoreV1().Namespaces().Delete(ctx, mceNamespace, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return stepResult{done: true}, nil
		}
		if err != nil {
			return stepResult{}, fmt.Errorf("delete namespace %q: %w", mceNamespace, err)
		}
		return stepResult{done: false, reason: "delete issued; waiting for namespace to be removed", values: []any{"namespace", mceNamespace}}, nil
	}

	return stepResult{done: false, reason: "namespace is terminating", values: []any{"namespace", ns}}, nil
}

func (c *Controller) ensureAppliedManifestWorksDeleted(ctx context.Context) (stepResult, error) {
	list, err := c.dynamicClient.Resource(AppliedManifestWorksGVR).List(ctx, metav1.ListOptions{})
	if apiGone(err) {
		return stepResult{done: true}, nil
	}
	if err != nil {
		return stepResult{}, fmt.Errorf("list AppliedManifestWorks: %w", err)
	}
	if len(list.Items) == 0 {
		return stepResult{done: true}, nil
	}

	names := make([]string, 0, len(list.Items))
	for i := range list.Items {
		names = append(names, list.Items[i].GetName())
		if err := c.deleteAppliedManifestWork(ctx, &list.Items[i]); err != nil {
			return stepResult{}, err
		}
	}
	return stepResult{done: false, reason: "AppliedManifestWorks still present", values: []any{"remaining", len(names), "names", names}}, nil
}

func (c *Controller) deleteAppliedManifestWork(ctx context.Context, obj *unstructured.Unstructured) error {
	name := obj.GetName()
	logger := utils.LoggerFromContext(ctx).WithValues("appliedmanifestwork", name)

	if len(obj.GetFinalizers()) > 0 {
		logger.Info("removing AppliedManifestWork finalizers", "finalizers", obj.GetFinalizers())
		updated := obj.DeepCopy()
		updated.SetFinalizers(nil)
		var err error
		obj, err = c.dynamicClient.Resource(AppliedManifestWorksGVR).Update(ctx, updated, metav1.UpdateOptions{})
		if apiGone(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("clear AppliedManifestWork %q finalizers: %w", name, err)
		}
	}

	if obj.GetDeletionTimestamp().IsZero() {
		logger.Info("deleting AppliedManifestWork")
		err := c.dynamicClient.Resource(AppliedManifestWorksGVR).Delete(ctx, name, metav1.DeleteOptions{})
		if apiGone(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("delete AppliedManifestWork %q: %w", name, err)
		}
	}
	return nil
}

func (c *Controller) ensureOCMCRDsDeleted(ctx context.Context) (stepResult, error) {
	list, err := c.dynamicClient.Resource(CustomResourceDefinitionsGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return stepResult{}, fmt.Errorf("list CustomResourceDefinitions: %w", err)
	}

	var targets []unstructured.Unstructured
	for i := range list.Items {
		if strings.HasSuffix(list.Items[i].GetName(), ocmCRDSuffix) {
			targets = append(targets, list.Items[i])
		}
	}
	if len(targets) == 0 {
		return stepResult{done: true}, nil
	}

	logger := utils.LoggerFromContext(ctx)
	names := make([]string, 0, len(targets))
	for i := range targets {
		crd := &targets[i]
		names = append(names, crd.GetName())
		if crd.GetDeletionTimestamp().IsZero() {
			logger.Info("deleting open-cluster-management CRD", "crd", crd.GetName())
			err := c.dynamicClient.Resource(CustomResourceDefinitionsGVR).Delete(ctx, crd.GetName(), metav1.DeleteOptions{})
			if apierrors.IsNotFound(err) {
				continue
			}
			if err != nil {
				return stepResult{}, fmt.Errorf("delete CRD %q: %w", crd.GetName(), err)
			}
		}
	}
	return stepResult{done: false, reason: "open-cluster-management CRDs still present", values: []any{"remaining", len(names), "names", names}}, nil
}

func apiGone(err error) bool {
	return apierrors.IsNotFound(err) || meta.IsNoMatchError(err)
}
