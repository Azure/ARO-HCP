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

package dataplane

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/controller"
	informers "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/informers/externalversions/sessiongate/v1alpha1"
	listers "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/listers/sessiongate/v1alpha1"
)

// data plane controller implementation.
// it runs on all replicas and registers sessions with the proxy registry, so that any replica can
// proxy traffic for a session.
type Controller struct {
	logger             klog.Logger
	registry           controller.SessionRegistry
	credentialProvider controller.CredentialProvider
	sessionsLister     listers.SessionLister
	sessionsSynced     cache.InformerSynced
	workqueue          workqueue.TypedRateLimitingInterface[cache.ObjectName]
}

func NewController(
	ctx context.Context,
	logger klog.Logger,
	sessionsInformer informers.SessionInformer,
	registry controller.SessionRegistry,
	credentialProvider controller.CredentialProvider,
) (*Controller, error) {
	rateLimiter := workqueue.NewTypedItemExponentialFailureRateLimiter[cache.ObjectName](
		100*time.Millisecond, // base delay
		60*time.Second,       // max delay
	)

	c := &Controller{
		logger:             logger,
		registry:           registry,
		credentialProvider: credentialProvider,
		sessionsLister:     sessionsInformer.Lister(),
		sessionsSynced:     sessionsInformer.Informer().HasSynced,
		workqueue:          workqueue.NewTypedRateLimitingQueue(rateLimiter),
	}

	logger.V(2).Info("Setting up event handlers for data plane controller")

	// Session Informer for data plane
	// Enqueues session keys to be processed by workers
	if _, err := sessionsInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: c.enqueueSession,
		UpdateFunc: func(old, new interface{}) {
			c.enqueueSession(new)
		},
		DeleteFunc: c.enqueueSession,
	}); err != nil {
		return nil, fmt.Errorf("failed to add event handler for sessions (data plane): %w", err)
	}

	return c, nil
}

// Run starts the data plane controller and blocks until the context is cancelled.
// Note: Informer caches are guaranteed to be synced before this is called (synchronized in cmd/options.go).
func (c *Controller) Run(ctx context.Context) error {
	defer c.workqueue.ShutDown()

	c.logger.V(2).Info("Starting data plane controller... waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(ctx.Done(), c.sessionsSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	// start a single worker goroutine
	// since we're just maintaining local in-memory state, one worker is sufficient
	go c.runWorker(ctx)

	c.logger.Info("Data plane controller started successfully")

	// block until context is cancelled
	<-ctx.Done()
	c.logger.V(2).Info("Data plane controller shutting down")

	return nil
}

// runWorker processes items from the workqueue
func (c *Controller) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem processes a single item from the workqueue
func (c *Controller) processNextWorkItem(ctx context.Context) bool {
	objRef, shutdown := c.workqueue.Get()
	logger := klog.LoggerWithValues(c.logger, "session", objRef)

	if shutdown {
		return false
	}
	defer c.workqueue.Done(objRef)

	err := c.syncHandler(ctx, logger, objRef)
	if err == nil {
		c.workqueue.Forget(objRef)
		logger.V(6).Info("Successfully synced")
		return true
	}

	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", objRef)
	c.workqueue.AddRateLimited(objRef)
	klog.ErrorS(err, "Error syncing session, will retry")

	return true
}

// enqueueSession extracts the key from the object and adds it to the workqueue
func (c *Controller) enqueueSession(obj interface{}) {
	// handle tombstones for Delete events
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}

	if objectRef, err := cache.ObjectToName(obj); err != nil {
		utilruntime.HandleError(err)
		return
	} else {
		c.workqueue.Add(objectRef)
	}
}

// syncHandler processes a single session from the workqueue
// it returns an error if the sync should be retried, nil otherwise
func (c *Controller) syncHandler(ctx context.Context, logger klog.Logger, objRef cache.ObjectName) error {
	session, err := c.sessionsLister.Sessions(objRef.Namespace).Get(objRef.Name)
	if err != nil {
		if errors.IsNotFound(err) {
			c.registry.UnregisterSession(objRef.Name)
			logger.V(2).Info("Unregistered deleted session from local registry")
			return nil
		}
		return fmt.Errorf("failed to get session from lister: %w", err)
	}

	// validate session is ready for registration
	if ready, reason := c.isReadyForRegistration(session); !ready {
		c.registry.UnregisterSession(session.Name)
		logger.V(2).Info("Unregistered session from local registry", "reason", reason)
		return nil
	}

	// register the session with the local registry for proxying traffic
	return c.registerSession(ctx, logger, session)
}

// isReadyForRegistration validates whether a session should be registered
func (c *Controller) isReadyForRegistration(session *sessiongatev1alpha1.Session) (bool, string) {
	if !session.DeletionTimestamp.IsZero() {
		return false, "being deleted"
	}
	if !session.IsReady() {
		return false, "not ready"
	}
	if session.Status.CredentialsSecretRef == "" {
		return false, "no credentials secret reference"
	}
	if session.Status.BackendKASURL == "" {
		return false, "no backend KAS URL"
	}
	return true, ""
}

// registerSession fetches credentials and registers the session in the local registry for proxying traffic
func (c *Controller) registerSession(ctx context.Context, logger klog.Logger, session *sessiongatev1alpha1.Session) error {
	credRef := controller.CredentialReference{
		SecretName:    session.Status.CredentialsSecretRef,
		BackendKASURL: session.Status.BackendKASURL,
	}

	restConfig, _, err := c.credentialProvider.GetCredentialsFromSecret(ctx, credRef)
	if err != nil {
		return fmt.Errorf("failed to get credentials from secret: %w", err)
	}

	endpoint, err := c.registry.RegisterSession(controller.NewSessionOptions(
		session.Name,
		session.Status.BackendKASURL,
		restConfig,
	))
	if err != nil {
		return fmt.Errorf("failed to register session: %w", err)
	}

	logger.V(2).Info("Registered session in local registry",
		"endpoint", endpoint,
		"backendKASURL", session.Status.BackendKASURL)

	return nil
}
