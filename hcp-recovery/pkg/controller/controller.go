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
	"errors"
	"fmt"
	"time"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	applyv1 "k8s.io/client-go/applyconfigurations/meta/v1"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	hcprecoveryv1alpha1 "github.com/Azure/ARO-HCP/hcp-recovery/pkg/apis/hcprecovery/v1alpha1"
	hcprecoveryapply "github.com/Azure/ARO-HCP/hcp-recovery/pkg/generated/applyconfiguration/hcprecovery/v1alpha1"
	hcprecoveryclient "github.com/Azure/ARO-HCP/hcp-recovery/pkg/generated/clientset/versioned"
	hcprecoveryinformers "github.com/Azure/ARO-HCP/hcp-recovery/pkg/generated/informers/externalversions"
)

type eventInfo struct {
	Reason, MessageFmt string
	Args               []interface{}
}

func event(reason, messageFmt string, args ...interface{}) *eventInfo {
	return &eventInfo{
		Reason:     reason,
		MessageFmt: messageFmt,
		Args:       args,
	}
}

// HCPRecoveryController reconciles HCPRecovery custom resources.
type HCPRecoveryController struct {
	namespace            string
	workqueue            workqueue.TypedRateLimitingInterface[cache.ObjectName]
	cachesToSync         []cache.InformerSynced
	kubeClient           kubernetes.Interface
	ctrlClient           ctrlclient.Client
	hcpRecoveryClient    hcprecoveryclient.Interface
	hcpRecoveryInformers hcprecoveryinformers.SharedInformerFactory

	eventRecorder record.EventRecorder

	getHCPRecovery func(namespace, name string) (*hcprecoveryv1alpha1.HCPRecovery, error)
}

func NewHCPRecoveryController(
	kubeClient kubernetes.Interface,
	ctrlClient ctrlclient.Client,
	hcpRecoveryClient hcprecoveryclient.Interface,
	hcpRecoveryInformers hcprecoveryinformers.SharedInformerFactory,
	kubeInformers kubeinformers.SharedInformerFactory,
	namespace string,
	eventRecorder record.EventRecorder,
) (*HCPRecoveryController, error) {
	workQueue := workqueue.NewTypedRateLimitingQueueWithConfig(
		workqueue.DefaultTypedControllerRateLimiter[cache.ObjectName](),
		workqueue.TypedRateLimitingQueueConfig[cache.ObjectName]{
			Name: "HCPRecoveryController",
		},
	)

	// HCPRecovery informer hookup
	hcpRecoveryInformer := hcpRecoveryInformers.Hcprecovery().V1alpha1().HCPRecoveries().Informer()
	if err := registerInformer(hcpRecoveryInformer, keyForObject, workQueue); err != nil {
		return nil, fmt.Errorf("failed to register hcprecovery informer: %w", err)
	}

	return &HCPRecoveryController{
		workqueue: workQueue,
		cachesToSync: []cache.InformerSynced{
			hcpRecoveryInformer.HasSynced,
		},
		kubeClient:           kubeClient,
		ctrlClient:           ctrlClient,
		hcpRecoveryClient:    hcpRecoveryClient,
		hcpRecoveryInformers: hcpRecoveryInformers,
		namespace:            namespace,
		eventRecorder:        eventRecorder,
		getHCPRecovery: func(namespace, name string) (*hcprecoveryv1alpha1.HCPRecovery, error) {
			return hcpRecoveryInformers.Hcprecovery().V1alpha1().HCPRecoveries().Lister().HCPRecoveries(namespace).Get(name)
		},
	}, nil
}

// Run starts the controller workers and blocks until the context is cancelled.
func (c *HCPRecoveryController) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	klog.InfoS("Starting hcp-recovery controller... waiting for informer caches to sync")

	if ok := cache.WaitForCacheSync(ctx.Done(), c.cachesToSync...); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.InfoS("Starting workers", "count", workers)
	for range workers {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	klog.InfoS("Started workers")
	<-ctx.Done()
	klog.InfoS("Shutting down workers")

	return nil
}

func (c *HCPRecoveryController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *HCPRecoveryController) processNextWorkItem(ctx context.Context) bool {
	objRef, shutdown := c.workqueue.Get()
	if shutdown {
		return false
	}
	defer c.workqueue.Done(objRef)

	recovery, err := c.getHCPRecovery(objRef.Namespace, objRef.Name)
	if err != nil && apierrors.IsNotFound(err) {
		// resource is gone, nothing to do
		return true
	} else if err != nil {
		c.workqueue.AddRateLimited(objRef)
		return true
	}

	err = c.syncRecovery(ctx, recovery)
	if err != nil {
		utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry")
		c.workqueue.AddRateLimited(objRef)
		return true
	}
	c.workqueue.Forget(objRef)
	return true
}

func (c *HCPRecoveryController) syncRecovery(ctx context.Context, recovery *hcprecoveryv1alpha1.HCPRecovery) error {
	logger := klog.FromContext(ctx).WithValues(
		"hcprecovery", recovery.Name,
		"namespace", recovery.Namespace,
	)
	ctx = klog.NewContext(ctx, logger)

	logger.Info("start sync")
	defer logger.Info("end sync")

	action, err := c.process(ctx, recovery)
	if err != nil {
		logger.Error(err, "Error processing HCPRecovery")
		return err
	}
	if action != nil {
		if err = action.validate(); err != nil {
			panic(err)
		}
		if action.Event != nil {
			c.eventRecorder.Eventf(recovery, v1.EventTypeNormal, action.Event.Reason, action.Event.MessageFmt, action.Event.Args...)
		}

		switch {
		case action.StatusUpdate != nil:
			_, err = c.hcpRecoveryClient.HcprecoveryV1alpha1().HCPRecoveries(recovery.Namespace).ApplyStatus(ctx, action.StatusUpdate, metav1.ApplyOptions{FieldManager: ControllerAgentName})
		case action.PatchHostedCluster != nil:
			err = c.ctrlClient.Patch(ctx, action.PatchHostedCluster.object, ctrlclient.MergeFrom(action.PatchHostedCluster.base))
		case action.DeleteHcpNamespace != nil:
			err = c.kubeClient.CoreV1().Namespaces().Delete(ctx, action.DeleteHcpNamespace.Name, metav1.DeleteOptions{})
		case len(action.RemoveCloudResourceFinalizers) > 0:
			for _, removal := range action.RemoveCloudResourceFinalizers {
				if err = c.ctrlClient.Patch(ctx, removal.object, ctrlclient.MergeFrom(removal.base)); err != nil {
					break
				}
			}
		case len(action.RemoveDeploymentResourceFinalizers) > 0:
			for _, removal := range action.RemoveDeploymentResourceFinalizers {
				_, err := c.kubeClient.AppsV1().Deployments(removal.object.GetNamespace()).Patch(ctx, removal.object.GetName(), types.MergePatchType, []byte(fmt.Sprintf(`{"metadata":{"finalizers":null}}`)), metav1.PatchOptions{})
				if err != nil {
					break
				}
			}
		case action.CreateVeleroRestore != nil:
			err = c.ctrlClient.Create(ctx, action.CreateVeleroRestore)
		case len(action.PatchVeleroSchedules) > 0:
			for i := range action.PatchVeleroSchedules {
				if err = c.ctrlClient.Update(ctx, &action.PatchVeleroSchedules[i]); err != nil {
					break
				}
			}
		}
		if err != nil {
			logger.Error(err, "Error executing action")
			return err
		}

		// Requeue immediately so the next step runs without waiting for relist
		c.workqueue.Add(cache.ObjectName{Namespace: recovery.Namespace, Name: recovery.Name})
	}
	return nil
}

type hostedClusterPatch struct {
	object *v1beta1.HostedCluster
	base   ctrlclient.Object
}

type finalizerRemoval struct {
	object ctrlclient.Object
	base   ctrlclient.Object
}

type actions struct {
	Event                              *eventInfo
	StatusUpdate                       *hcprecoveryapply.HCPRecoveryApplyConfiguration
	PatchHostedCluster                 *hostedClusterPatch
	DeleteHcpNamespace                 *v1.Namespace
	RemoveCloudResourceFinalizers      []finalizerRemoval
	RemoveDeploymentResourceFinalizers []finalizerRemoval
	CreateVeleroRestore                *velerov1api.Restore
	PatchVeleroSchedules               []velerov1api.Schedule
}

func (a *actions) validate() error {
	var set int
	if a.StatusUpdate != nil {
		set += 1
	}
	if a.PatchHostedCluster != nil {
		set += 1
	}
	if a.DeleteHcpNamespace != nil {
		set += 1
	}
	if len(a.RemoveCloudResourceFinalizers) > 0 {
		set += 1
	}
	if len(a.RemoveDeploymentResourceFinalizers) > 0 {
		set += 1
	}
	if a.CreateVeleroRestore != nil {
		set += 1
	}
	if len(a.PatchVeleroSchedules) > 0 {
		set += 1
	}
	if set > 1 {
		return errors.New("programmer error: more than one action set")
	}
	return nil
}

func (c *HCPRecoveryController) process(ctx context.Context, recovery *hcprecoveryv1alpha1.HCPRecovery) (*actions, error) {
	logger := klog.FromContext(ctx)
	logger.V(4).Info("Reconciling HCPRecovery resource")

	steps := []recoveryStep{
		c.validateBackup,
		c.pauseBackupSchedule,
		c.pauseHostedCluster,
		c.deleteHcpNamespace,
		c.removeCloudResourcesFinalizers,
		c.removeDeploymentResourceFinalizers,
		c.waitForNamespaceDeletion,
		c.createVeleroRestore,
		c.validateHostedCluster,
		c.unpauseBackupSchedule,
	}
	for _, step := range steps {
		done, action, err := step(ctx, recovery)
		if done {
			return action, err
		}
	}
	return nil, nil
}

type recoveryStep func(ctx context.Context, recovery *hcprecoveryv1alpha1.HCPRecovery) (bool, *actions, error)

// handleTransientError returns the error to trigger a requeue with rate limiting.
func (c *HCPRecoveryController) handleTransientError(err error) (bool, *actions, error) {
	return true, nil, err
}

// handlePermanentError sets a condition but does not actively requeue.
// The informer will passively re-sync when the condition update is applied
// and on every relist interval.
func (c *HCPRecoveryController) handlePermanentError(recovery *hcprecoveryv1alpha1.HCPRecovery, condition *applyv1.ConditionApplyConfiguration) (bool, *actions, error) {
	statusUpdate, needsUpdate := NewStatus(recovery.Status).
		WithConditions(condition).AsApplyConfiguration(recovery)
	if needsUpdate {
		return true, &actions{StatusUpdate: statusUpdate}, nil
	}
	return true, nil, nil
}

// handleRetryableError sets a condition if necessary (which triggers a re-sync
// via the informer), otherwise returns the error to requeue with rate limiting.
func (c *HCPRecoveryController) handleRetryableError(recovery *hcprecoveryv1alpha1.HCPRecovery, condition *applyv1.ConditionApplyConfiguration, err error) (bool, *actions, error) {
	statusUpdate, needsUpdate := NewStatus(recovery.Status).
		WithConditions(condition).AsApplyConfiguration(recovery)
	if needsUpdate {
		return true, &actions{StatusUpdate: statusUpdate}, nil
	}
	return true, nil, err
}

func restoreName(recoveryName string) string {
	return fmt.Sprintf("restore-%s", recoveryName)
}

func (c *HCPRecoveryController) getHostedCluster(ctx context.Context, clusterId string) (*v1beta1.HostedCluster, error) {
	hostedClusters := &v1beta1.HostedClusterList{}
	if err := c.ctrlClient.List(ctx, hostedClusters, ctrlclient.MatchingLabels{"api.openshift.com/id": clusterId}); err != nil {
		return nil, err
	}
	if len(hostedClusters.Items) == 0 {
		return nil, nil
	}
	if len(hostedClusters.Items) > 1 {
		return nil, fmt.Errorf("multiple hosted clusters found for cluster %s", clusterId)
	}
	return &hostedClusters.Items[0], nil
}
