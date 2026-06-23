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

package ksmhcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	appsac "k8s.io/client-go/applyconfigurations/apps/v1"
	coreac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hcpinformers "github.com/openshift/hypershift/client/informers/externalversions/hypershift/v1beta1"
	hcplisters "github.com/openshift/hypershift/client/listers/hypershift/v1beta1"
)

const (
	resourceName = "kube-state-metrics-hcp"
	labelApp     = "app.kubernetes.io/name"
	fieldManager = "mgmt-agent-ksm-hcp"

	// LabelSelector filters informers to only cache resources managed by this controller.
	LabelSelector = labelApp + "=" + resourceName

	// serviceNetworkKubeconfigSecret is the well-known secret name created by
	// HyperShift's control-plane-operator (manifests/kas.go:KASServiceKubeconfigSecret)
	// for in-cluster access to the HCP kube-apiserver. Used by 25+ HCP components.
	serviceNetworkKubeconfigSecret = "service-network-admin-kubeconfig"
	serviceNetworkKubeconfigKey    = "kubeconfig"
)

var ServiceMonitorGVR = schema.GroupVersionResource{
	Group:    "monitoring.coreos.com",
	Version:  "v1",
	Resource: "servicemonitors",
}

// KSMHCPController watches HostedControlPlane objects and ensures a
// kube-state-metrics Deployment, Service, and ServiceMonitor exists in each
// HCP namespace to scrape customer node metrics from the HCP kube-apiserver.
type KSMHCPController struct {
	kubeClientset kubernetes.Interface
	dynamicClient dynamic.Interface
	hcpLister     hcplisters.HostedControlPlaneLister
	hasSynced     []cache.InformerSynced
	workqueue     workqueue.TypedRateLimitingInterface[string]
	ksmImage      string
}

// NewKSMHCPController creates a new KSMHCPController.
func NewKSMHCPController(
	kubeClientset kubernetes.Interface,
	dynamicClient dynamic.Interface,
	hcpInformer hcpinformers.HostedControlPlaneInformer,
	deploymentInformer cache.SharedIndexInformer,
	serviceInformer cache.SharedIndexInformer,
	serviceMonitorInformer cache.SharedIndexInformer,
	ksmImage string,
) (*KSMHCPController, error) {
	c := &KSMHCPController{
		kubeClientset: kubeClientset,
		dynamicClient: dynamicClient,
		hcpLister:     hcpInformer.Lister(),
		hasSynced: []cache.InformerSynced{
			hcpInformer.Informer().HasSynced,
			deploymentInformer.HasSynced,
			serviceInformer.HasSynced,
			serviceMonitorInformer.HasSynced,
		},
		workqueue: workqueue.NewTypedRateLimitingQueueWithConfig(workqueue.DefaultTypedControllerRateLimiter[string](), workqueue.TypedRateLimitingQueueConfig[string]{Name: "KSMHCP"}),
		ksmImage:  ksmImage,
	}

	enqueueHCP := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				c.workqueue.Add(key)
			}
		},
		UpdateFunc: func(old, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(new)
			if err == nil {
				c.workqueue.Add(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				c.workqueue.Add(key)
			}
		},
	}

	if _, err := hcpInformer.Informer().AddEventHandler(enqueueHCP); err != nil {
		return nil, fmt.Errorf("failed to add HCP event handler: %w", err)
	}

	enqueueOwner := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			c.enqueueForOwner(obj)
		},
		UpdateFunc: func(old, new interface{}) {
			c.enqueueForOwner(new)
		},
		DeleteFunc: func(obj interface{}) {
			tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
			if ok {
				obj = tombstone.Obj
			}
			c.enqueueForOwner(obj)
		},
	}

	if _, err := deploymentInformer.AddEventHandler(enqueueOwner); err != nil {
		return nil, fmt.Errorf("failed to add Deployment event handler: %w", err)
	}
	if _, err := serviceInformer.AddEventHandler(enqueueOwner); err != nil {
		return nil, fmt.Errorf("failed to add Service event handler: %w", err)
	}
	if _, err := serviceMonitorInformer.AddEventHandler(enqueueOwner); err != nil {
		return nil, fmt.Errorf("failed to add ServiceMonitor event handler: %w", err)
	}

	return c, nil
}

func (c *KSMHCPController) enqueueForOwner(obj interface{}) {
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return
	}
	for _, ref := range accessor.GetOwnerReferences() {
		if ref.Kind == "HostedControlPlane" && ref.APIVersion == "hypershift.openshift.io/v1beta1" {
			c.workqueue.Add(accessor.GetNamespace() + "/" + ref.Name)
			return
		}
	}
}

// Run starts the controller workers and blocks until the context is cancelled.
func (c *KSMHCPController) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	logger := klog.FromContext(ctx)
	logger.Info("Starting KSM HCP controller")

	logger.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(ctx.Done(), c.hasSynced...); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	logger.Info("Starting workers", "count", workers)
	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	logger.Info("KSM HCP controller started")
	<-ctx.Done()
	logger.Info("Shutting down KSM HCP controller")

	return nil
}

func (c *KSMHCPController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *KSMHCPController) processNextWorkItem(ctx context.Context) bool {
	key, shutdown := c.workqueue.Get()
	if shutdown {
		return false
	}
	defer c.workqueue.Done(key)

	err := c.syncHandler(ctx, key)
	if err == nil {
		c.workqueue.Forget(key)
		return true
	}

	utilruntime.HandleError(fmt.Errorf("error syncing HostedControlPlane %q: %w", key, err))
	c.workqueue.AddRateLimited(key)
	return true
}

func (c *KSMHCPController) syncHandler(ctx context.Context, key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return fmt.Errorf("invalid key %q: %w", key, err)
	}

	logger := klog.FromContext(ctx).WithValues("namespace", namespace, "name", name)
	ctx = klog.NewContext(ctx, logger)

	hcp, err := c.hcpLister.HostedControlPlanes(namespace).Get(name)
	if apierrors.IsNotFound(err) {
		logger.V(4).Info("HostedControlPlane deleted, resources will be garbage collected via OwnerReference")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get HostedControlPlane %q: %w", key, err)
	}

	if !hcp.DeletionTimestamp.IsZero() {
		logger.V(4).Info("HostedControlPlane is being deleted, letting GC clean up KSM resources")
		return nil
	}

	if !isKubeAPIServerAvailable(hcp) {
		logger.V(4).Info("KubeAPIServer not yet available, skipping until next informer event")
		return nil
	}

	return c.reconcile(ctx, hcp)
}

func (c *KSMHCPController) reconcile(ctx context.Context, hcp *hypershiftv1beta1.HostedControlPlane) error {
	logger := klog.FromContext(ctx)
	ns := hcp.Namespace

	ownerRef := metav1.OwnerReference{
		APIVersion: "hypershift.openshift.io/v1beta1",
		Kind:       "HostedControlPlane",
		Name:       hcp.Name,
		UID:        hcp.UID,
	}

	deployment := buildDeployment(ns, c.ksmImage, serviceNetworkKubeconfigSecret, serviceNetworkKubeconfigKey, ownerRef)
	if err := c.applyDeployment(ctx, deployment); err != nil {
		return fmt.Errorf("failed to apply deployment in %s: %w", ns, err)
	}

	service := buildService(ns, ownerRef)
	if err := c.applyService(ctx, service); err != nil {
		return fmt.Errorf("failed to apply service in %s: %w", ns, err)
	}

	serviceMonitor, err := buildServiceMonitor(ns, ownerRef)
	if err != nil {
		return fmt.Errorf("failed to build servicemonitor in %s: %w", ns, err)
	}
	if err := c.applyServiceMonitor(ctx, serviceMonitor); err != nil {
		return fmt.Errorf("failed to apply servicemonitor in %s: %w", ns, err)
	}

	logger.Info("Reconciled KSM resources for HostedControlPlane")
	return nil
}

func (c *KSMHCPController) applyDeployment(ctx context.Context, desired *appsac.DeploymentApplyConfiguration) error {
	_, err := c.kubeClientset.AppsV1().Deployments(*desired.Namespace).Apply(
		ctx, desired, metav1.ApplyOptions{FieldManager: fieldManager, Force: true},
	)
	return err
}

func (c *KSMHCPController) applyService(ctx context.Context, desired *coreac.ServiceApplyConfiguration) error {
	_, err := c.kubeClientset.CoreV1().Services(*desired.Namespace).Apply(
		ctx, desired, metav1.ApplyOptions{FieldManager: fieldManager, Force: true},
	)
	return err
}

func (c *KSMHCPController) applyServiceMonitor(ctx context.Context, desired *unstructured.Unstructured) error {
	data, err := json.Marshal(desired)
	if err != nil {
		return fmt.Errorf("failed to marshal servicemonitor: %w", err)
	}
	_, err = c.dynamicClient.Resource(ServiceMonitorGVR).Namespace(desired.GetNamespace()).Patch(
		ctx, desired.GetName(), types.ApplyPatchType, data,
		metav1.PatchOptions{FieldManager: fieldManager, Force: ptr.To(true)},
	)
	return err
}

func isKubeAPIServerAvailable(hcp *hypershiftv1beta1.HostedControlPlane) bool {
	for _, c := range hcp.Status.Conditions {
		if c.Type == "KubeAPIServerAvailable" {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}
