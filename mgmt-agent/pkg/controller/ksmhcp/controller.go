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
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hcpinformers "github.com/openshift/hypershift/client/informers/externalversions/hypershift/v1beta1"
	hcplisters "github.com/openshift/hypershift/client/listers/hypershift/v1beta1"
)

const (
	resourceName = "kube-state-metrics-hcp"
	labelApp     = "app.kubernetes.io/name"
	fieldManager = "mgmt-agent-ksm-hcp"
)

var serviceMonitorGVR = schema.GroupVersionResource{
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
	hcpSynced     cache.InformerSynced
	workqueue     workqueue.TypedRateLimitingInterface[string]
	ksmImage      string
}

// NewKSMHCPController creates a new KSMHCPController.
func NewKSMHCPController(
	kubeClientset kubernetes.Interface,
	dynamicClient dynamic.Interface,
	hcpInformer hcpinformers.HostedControlPlaneInformer,
	ksmImage string,
) (*KSMHCPController, error) {
	c := &KSMHCPController{
		kubeClientset: kubeClientset,
		dynamicClient: dynamicClient,
		hcpLister:     hcpInformer.Lister(),
		hcpSynced:     hcpInformer.Informer().HasSynced,
		workqueue:     workqueue.NewTypedRateLimitingQueueWithConfig(workqueue.DefaultTypedControllerRateLimiter[string](), workqueue.TypedRateLimitingQueueConfig[string]{Name: "KSMHCP"}),
		ksmImage:      ksmImage,
	}

	if _, err := hcpInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
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
	}); err != nil {
		return nil, fmt.Errorf("failed to add event handler: %w", err)
	}

	return c, nil
}

// Run starts the controller workers and blocks until the context is cancelled.
func (c *KSMHCPController) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	logger := klog.FromContext(ctx)
	logger.Info("Starting KSM HCP controller")

	logger.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(ctx.Done(), c.hcpSynced); !ok {
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
	logger := klog.FromContext(ctx)

	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return fmt.Errorf("invalid key %q: %w", key, err)
	}

	hcp, err := c.hcpLister.HostedControlPlanes(namespace).Get(name)
	if apierrors.IsNotFound(err) {
		logger.V(4).Info("HostedControlPlane deleted, resources will be garbage collected via OwnerReference", "key", key)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get HostedControlPlane %q: %w", key, err)
	}

	if hcp.Status.KubeConfig == nil {
		logger.V(4).Info("HostedControlPlane kubeconfig not yet available, requeueing", "key", key)
		return fmt.Errorf("kubeconfig not ready for %q", key)
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

	deployment := buildDeployment(ns, c.ksmImage, hcp.Status.KubeConfig, ownerRef)
	if err := c.ensureDeployment(ctx, deployment); err != nil {
		return fmt.Errorf("failed to ensure deployment in %s: %w", ns, err)
	}

	service := buildService(ns, ownerRef)
	if err := c.ensureService(ctx, service); err != nil {
		return fmt.Errorf("failed to ensure service in %s: %w", ns, err)
	}

	serviceMonitor := buildServiceMonitor(ns, ownerRef)
	if err := c.ensureServiceMonitor(ctx, serviceMonitor); err != nil {
		return fmt.Errorf("failed to ensure servicemonitor in %s: %w", ns, err)
	}

	logger.Info("Reconciled KSM resources for HostedControlPlane", "namespace", ns, "name", hcp.Name)
	return nil
}

func (c *KSMHCPController) ensureDeployment(ctx context.Context, desired *appsv1.Deployment) error {
	existing, err := c.kubeClientset.AppsV1().Deployments(desired.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = c.kubeClientset.AppsV1().Deployments(desired.Namespace).Create(ctx, desired, metav1.CreateOptions{FieldManager: fieldManager})
		return err
	}
	if err != nil {
		return err
	}

	if !equality.Semantic.DeepEqual(existing.Spec, desired.Spec) {
		existing.Spec = desired.Spec
		_, err = c.kubeClientset.AppsV1().Deployments(desired.Namespace).Update(ctx, existing, metav1.UpdateOptions{FieldManager: fieldManager})
		return err
	}

	return nil
}

func (c *KSMHCPController) ensureService(ctx context.Context, desired *corev1.Service) error {
	existing, err := c.kubeClientset.CoreV1().Services(desired.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = c.kubeClientset.CoreV1().Services(desired.Namespace).Create(ctx, desired, metav1.CreateOptions{FieldManager: fieldManager})
		return err
	}
	if err != nil {
		return err
	}

	if !equality.Semantic.DeepEqual(existing.Spec.Ports, desired.Spec.Ports) ||
		!equality.Semantic.DeepEqual(existing.Spec.Selector, desired.Spec.Selector) {
		existing.Spec.Ports = desired.Spec.Ports
		existing.Spec.Selector = desired.Spec.Selector
		_, err = c.kubeClientset.CoreV1().Services(desired.Namespace).Update(ctx, existing, metav1.UpdateOptions{FieldManager: fieldManager})
		return err
	}

	return nil
}

func (c *KSMHCPController) ensureServiceMonitor(ctx context.Context, desired *unstructured.Unstructured) error {
	client := c.dynamicClient.Resource(serviceMonitorGVR).Namespace(desired.GetNamespace())

	_, err := client.Get(ctx, desired.GetName(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = client.Create(ctx, desired, metav1.CreateOptions{FieldManager: fieldManager})
		return err
	}
	if err != nil {
		return err
	}

	_, err = client.Update(ctx, desired, metav1.UpdateOptions{FieldManager: fieldManager})
	return err
}

// toUnstructured converts a runtime.Object to an Unstructured representation.
func toUnstructured(obj runtime.Object) (*unstructured.Unstructured, error) {
	data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: data}, nil
}
