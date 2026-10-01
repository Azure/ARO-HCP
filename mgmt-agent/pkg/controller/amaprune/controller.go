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

// Package amaprune removes the objects that only the AMA-mode controllers
// create, when the mgmt-agent is running in OSS mode. It is the T4 counterpart
// of the AMA-only monitortranslator and amanetpolicy controllers: switching a
// management cluster's monitoringApiGroup from AMA back to OSS must converge in
// a single rollout, which means the azmonitoring ServiceMonitors/PodMonitors the
// translator produced and the NetworkPolicies amanetpolicy created have to be
// cleaned up. Those controllers do not run in OSS mode, so nothing else would
// delete their output.
package amaprune

import (
	"context"
	"fmt"
	"math"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/amanetpolicy"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/monitortranslator"
)

const AMAPruneControllerName = "ama-prune"

// translatorOwnerAPIVersion is the ownerReference apiVersion the monitor
// translator stamps on every azmonitoring copy it creates (the source monitor is
// a monitoring.coreos.com/v1 object). It is the marker that distinguishes a
// translator copy from the kube-state-metrics azmonitoring monitor the ksmhcp
// controller emits directly, which is owned by the HostedControlPlane instead and
// is cleaned up by ksmhcp's own deleteStaleServiceMonitor. The pruner must not
// touch the latter.
const translatorOwnerAPIVersion = "monitoring.coreos.com/v1"

// Controller deletes AMA-only objects left behind on a management cluster that is
// (now) running in OSS mode. It is a one-shot reconciler: Run performs the prune
// once, retrying with backoff until it succeeds or the context is cancelled.
type Controller struct {
	dynamicClient dynamic.Interface
	kubeClient    kubernetes.Interface
}

// NewController creates a new AMA prune Controller.
func NewController(dynamicClient dynamic.Interface, kubeClient kubernetes.Interface) *Controller {
	return &Controller{
		dynamicClient: dynamicClient,
		kubeClient:    kubeClient,
	}
}

// Run performs the prune, retrying with capped exponential backoff until it
// succeeds or ctx is cancelled. It is a one-shot operation rather than a watch:
// the AMA-only objects are created only while the agent runs in AMA mode, so once
// they are gone in OSS mode nothing recreates them.
func (c *Controller) Run(ctx context.Context) error {
	defer utilruntime.HandleCrash()

	ctx = utils.ContextWithControllerName(ctx, AMAPruneControllerName)
	logger := klog.FromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(AMAPruneControllerName)...)
	ctx = klog.NewContext(ctx, logger)
	logger.Info("Starting AMA prune: removing AMA-only objects while running in OSS mode")

	backoff := wait.Backoff{
		Duration: time.Second,
		Factor:   2.0,
		Jitter:   0.1,
		Steps:    math.MaxInt32,
		Cap:      5 * time.Minute,
	}
	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		if err := c.pruneOnce(ctx); err != nil {
			logger.Error(err, "AMA prune attempt failed; will retry")
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("AMA prune did not complete: %w", err)
	}

	logger.Info("AMA prune completed")
	return nil
}

func (c *Controller) pruneOnce(ctx context.Context) error {
	if err := c.pruneTranslatedMonitors(ctx, monitortranslator.TargetServiceMonitorGVR); err != nil {
		return err
	}
	if err := c.pruneTranslatedMonitors(ctx, monitortranslator.TargetPodMonitorGVR); err != nil {
		return err
	}
	return c.pruneNetworkPolicies(ctx)
}

// pruneTranslatedMonitors deletes the azmonitoring monitors that carry a
// monitoring.coreos.com/v1 ownerReference (translator copies). A missing
// azmonitoring CRD (a pure-OSS cluster that never ran AMA) is not an error:
// there is nothing to prune.
func (c *Controller) pruneTranslatedMonitors(ctx context.Context, gvr schema.GroupVersionResource) error {
	logger := klog.FromContext(ctx)

	list, err := c.dynamicClient.Resource(gvr).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
			logger.V(4).Info("azmonitoring CRD not installed; nothing to prune", "resource", gvr.Resource)
			return nil
		}
		return fmt.Errorf("failed to list %s.%s: %w", gvr.Resource, gvr.Group, err)
	}

	for i := range list.Items {
		item := &list.Items[i]
		if !hasTranslatorOwner(item) {
			// Owned by the HostedControlPlane (the ksmhcp direct-emit monitor), not
			// by the translator; ksmhcp cleans that one up itself.
			continue
		}
		if err := c.dynamicClient.Resource(gvr).Namespace(item.GetNamespace()).Delete(ctx, item.GetName(), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete %s %s/%s: %w", gvr.Resource, item.GetNamespace(), item.GetName(), err)
		}
		logger.V(2).Info("Deleted translated AMA monitor", "resource", gvr.Resource, "namespace", item.GetNamespace(), "name", item.GetName())
	}
	return nil
}

// pruneNetworkPolicies deletes the NetworkPolicies the amanetpolicy controller
// creates, selected by its managed-by label.
func (c *Controller) pruneNetworkPolicies(ctx context.Context) error {
	logger := klog.FromContext(ctx)

	nps, err := c.kubeClient.NetworkingV1().NetworkPolicies(metav1.NamespaceAll).List(ctx, metav1.ListOptions{LabelSelector: amanetpolicy.LabelSelector})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to list AMA NetworkPolicies: %w", err)
	}

	for i := range nps.Items {
		np := &nps.Items[i]
		if err := c.kubeClient.NetworkingV1().NetworkPolicies(np.Namespace).Delete(ctx, np.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete NetworkPolicy %s/%s: %w", np.Namespace, np.Name, err)
		}
		logger.V(2).Info("Deleted AMA NetworkPolicy", "namespace", np.Namespace, "name", np.Name)
	}
	return nil
}

// hasTranslatorOwner reports whether obj carries a monitoring.coreos.com/v1
// ownerReference, which the monitor translator sets on every azmonitoring copy
// it creates.
func hasTranslatorOwner(obj *unstructured.Unstructured) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.APIVersion == translatorOwnerAPIVersion {
			return true
		}
	}
	return false
}
