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

package amanetpolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	metaac "k8s.io/client-go/applyconfigurations/meta/v1"
	networkingac "k8s.io/client-go/applyconfigurations/networking/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftclient "github.com/openshift/hypershift/client/clientset/clientset"
	hcpinformers "github.com/openshift/hypershift/client/informers/externalversions/hypershift/v1beta1"
	hcplisters "github.com/openshift/hypershift/client/listers/hypershift/v1beta1"
)

const (
	AMANetworkPolicyControllerName = "ama-network-policy"

	finalizerName     = "mgmt-agent.aro-hcp.azure.com/ama-netpolicy"
	networkPolicyName = "ama-metrics-allow"
	managedByLabel    = "app.kubernetes.io/managed-by"
	managedByValue    = "mgmt-agent-ama-netpolicy"
	fieldManager      = "mgmt-agent-ama-netpolicy"

	// LabelSelector filters the NetworkPolicy informer to only cache resources managed by this controller.
	LabelSelector = managedByLabel + "=" + managedByValue
)

// AMANetworkPolicyController watches HostedControlPlane objects and creates a
// NetworkPolicy in each HCP namespace allowing AMA metrics pods from
// kube-system to scrape Prometheus endpoints.
type AMANetworkPolicyController struct {
	kubeClientset kubernetes.Interface
	hsClient      hypershiftclient.Interface
	hcpLister     hcplisters.HostedControlPlaneLister
	hasSynced     []cache.InformerSynced
	workqueue     workqueue.TypedRateLimitingInterface[string]
}

// NewAMANetworkPolicyController creates a new AMANetworkPolicyController.
func NewAMANetworkPolicyController(
	kubeClientset kubernetes.Interface,
	hsClient hypershiftclient.Interface,
	hcpInformer hcpinformers.HostedControlPlaneInformer,
	networkPolicyInformer cache.SharedIndexInformer,
) (*AMANetworkPolicyController, error) {
	c := &AMANetworkPolicyController{
		kubeClientset: kubeClientset,
		hsClient:      hsClient,
		hcpLister:     hcpInformer.Lister(),
		hasSynced: []cache.InformerSynced{
			hcpInformer.Informer().HasSynced,
			networkPolicyInformer.HasSynced,
		},
		workqueue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{Name: AMANetworkPolicyControllerName},
		),
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

	if _, err := networkPolicyInformer.AddEventHandler(enqueueOwner); err != nil {
		return nil, fmt.Errorf("failed to add NetworkPolicy event handler: %w", err)
	}

	return c, nil
}

func (c *AMANetworkPolicyController) enqueueForOwner(obj interface{}) {
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
func (c *AMANetworkPolicyController) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	logger := klog.FromContext(ctx)
	logger.Info("Starting AMA NetworkPolicy controller")

	logger.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(ctx.Done(), c.hasSynced...); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	logger.Info("Starting workers", "count", workers)
	for range workers {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	logger.Info("AMA NetworkPolicy controller started")
	<-ctx.Done()
	logger.Info("Shutting down AMA NetworkPolicy controller")

	return nil
}

func (c *AMANetworkPolicyController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *AMANetworkPolicyController) processNextWorkItem(ctx context.Context) bool {
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

func (c *AMANetworkPolicyController) syncHandler(ctx context.Context, key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return fmt.Errorf("invalid key %q: %w", key, err)
	}

	logger := klog.FromContext(ctx).WithValues("namespace", namespace, "name", name)
	ctx = klog.NewContext(ctx, logger)

	hcp, err := c.hcpLister.HostedControlPlanes(namespace).Get(name)
	if apierrors.IsNotFound(err) {
		logger.V(4).Info("HostedControlPlane deleted, NetworkPolicy will be garbage collected via OwnerReference")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get HostedControlPlane %q: %w", key, err)
	}

	if !hcp.DeletionTimestamp.IsZero() {
		if err := c.kubeClientset.NetworkingV1().NetworkPolicies(namespace).Delete(ctx, networkPolicyName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete NetworkPolicy in %s: %w", namespace, err)
		}
		logger.V(4).Info("Deleted AMA NetworkPolicy for terminating HostedControlPlane")
		if err := c.removeFinalizer(ctx, hcp); err != nil {
			return fmt.Errorf("failed to remove finalizer from HostedControlPlane %q: %w", key, err)
		}
		return nil
	}

	if !slices.Contains(hcp.Finalizers, finalizerName) {
		if err := c.addFinalizer(ctx, hcp); err != nil {
			return fmt.Errorf("failed to add finalizer to HostedControlPlane %q: %w", key, err)
		}
		return nil
	}

	return c.reconcile(ctx, hcp)
}

func (c *AMANetworkPolicyController) patchFinalizers(ctx context.Context, hcp *hypershiftv1beta1.HostedControlPlane, finalizers []string) error {
	patch, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"finalizers": finalizers,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to marshal finalizer patch: %w", err)
	}
	_, err = c.hsClient.HypershiftV1beta1().HostedControlPlanes(hcp.Namespace).Patch(
		ctx, hcp.Name, types.MergePatchType, patch, metav1.PatchOptions{},
	)
	return err
}

func (c *AMANetworkPolicyController) addFinalizer(ctx context.Context, hcp *hypershiftv1beta1.HostedControlPlane) error {
	finalizers := append(hcp.Finalizers, finalizerName)
	return c.patchFinalizers(ctx, hcp, finalizers)
}

func (c *AMANetworkPolicyController) removeFinalizer(ctx context.Context, hcp *hypershiftv1beta1.HostedControlPlane) error {
	finalizers := slices.DeleteFunc(slices.Clone(hcp.Finalizers), func(s string) bool {
		return s == finalizerName
	})
	return c.patchFinalizers(ctx, hcp, finalizers)
}

func (c *AMANetworkPolicyController) reconcile(ctx context.Context, hcp *hypershiftv1beta1.HostedControlPlane) error {
	logger := klog.FromContext(ctx)

	np := BuildNetworkPolicy(hcp.Namespace, hcp.Name, hcp.UID)
	_, err := c.kubeClientset.NetworkingV1().NetworkPolicies(hcp.Namespace).Apply(
		ctx, np, metav1.ApplyOptions{FieldManager: fieldManager, Force: true},
	)
	if err != nil {
		return fmt.Errorf("failed to apply NetworkPolicy in %s: %w", hcp.Namespace, err)
	}

	logger.V(4).Info("Reconciled AMA NetworkPolicy for HostedControlPlane")
	return nil
}

// BuildNetworkPolicy constructs the SSA apply configuration for the AMA
// metrics NetworkPolicy. Exported for testing.
func BuildNetworkPolicy(namespace, hcpName string, hcpUID types.UID) *networkingac.NetworkPolicyApplyConfiguration {
	return networkingac.NetworkPolicy(networkPolicyName, namespace).
		WithLabels(map[string]string{
			managedByLabel: managedByValue,
		}).
		WithOwnerReferences(
			metaac.OwnerReference().
				WithAPIVersion("hypershift.openshift.io/v1beta1").
				WithKind("HostedControlPlane").
				WithName(hcpName).
				WithUID(hcpUID),
		).
		WithSpec(networkingac.NetworkPolicySpec().
			WithPodSelector(metaac.LabelSelector()).
			WithPolicyTypes(networkingv1.PolicyTypeIngress).
			WithIngress(networkingac.NetworkPolicyIngressRule().
				WithFrom(networkingac.NetworkPolicyPeer().
					WithNamespaceSelector(metaac.LabelSelector().
						WithMatchLabels(map[string]string{
							"kubernetes.io/metadata.name": "kube-system",
						}),
					).
					WithPodSelector(metaac.LabelSelector().
						WithMatchLabels(map[string]string{
							"rsName": "ama-metrics",
						}),
					),
				),
			),
		)
}
