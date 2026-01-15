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
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	sessiongateinformers "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/informers/externalversions"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/registry"
)

// data plane controller implementation.
// it runs on all replicas and registers sessions with the proxy registry, so that any replica can
// proxy traffic for a session.
type DataPlaneController struct {
	workqueue    workqueue.TypedRateLimitingInterface[cache.ObjectName]
	cachesToSync []cache.InformerSynced
	fieldManager string
	logger       klog.Logger
	registry     registry.SessionRegistry
	getSession   func(namespace, name string) (*sessiongatev1alpha1.Session, error)
	getSecret    func(namespace, name string) (*corev1.Secret, error)
}

func NewDataPlaneController(
	ctx context.Context,
	logger klog.Logger,
	sessiongateInformers sessiongateinformers.SharedInformerFactory,
	kubeInformers kubeinformers.SharedInformerFactory,
	registry registry.SessionRegistry,
	eventRecorder record.EventRecorder,
) (*DataPlaneController, error) {
	workQueue := workqueue.NewTypedRateLimitingQueueWithConfig(
		workqueue.DefaultTypedControllerRateLimiter[cache.ObjectName](),
		workqueue.TypedRateLimitingQueueConfig[cache.ObjectName]{
			Name: "DataPlaneControlPlaneController",
		},
	)
	c := &DataPlaneController{
		workqueue:    workQueue,
		cachesToSync: []cache.InformerSynced{},
		fieldManager: ControllerAgentName,
		logger:       logger,
		registry:     registry,
		getSession: func(namespace, name string) (*sessiongatev1alpha1.Session, error) {
			return sessiongateInformers.Sessiongate().V1alpha1().Sessions().Lister().Sessions(namespace).Get(name)
		},
		getSecret: func(namespace, name string) (*corev1.Secret, error) {
			return kubeInformers.Core().V1().Secrets().Lister().Secrets(namespace).Get(name)
		},
	}

	if err := registerInformer(sessiongateInformers.Sessiongate().V1alpha1().Sessions().Informer(), keyForSession, c.workqueue); err != nil {
		return nil, fmt.Errorf("failed to register session informer: %w", err)
	}

	return c, nil
}

func (c *DataPlaneController) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	klog.InfoS("Starting control plane controller... waiting for informer caches to sync")

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

func (c *DataPlaneController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *DataPlaneController) processNextWorkItem(ctx context.Context) bool {
	objRef, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	defer c.workqueue.Done(objRef)

	// reconcile the session
	err := c.syncSession(ctx, objRef.Namespace, objRef.Name)
	if err != nil {
		utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", objRef)
		c.workqueue.AddRateLimited(objRef)
		return true
	}
	c.workqueue.Forget(objRef)
	return true
}

func (c *DataPlaneController) syncSession(ctx context.Context, namespace, name string) error {
	session, err := c.getSession(namespace, name)
	if err != nil && apierrors.IsNotFound(err) {
		c.registry.UnregisterSession(name)
		return nil // nothing to be done, Session is gone
	} else if err != nil {
		return err
	}

	// validate session is ready for registration
	if ready, reason := c.isReadyForRegistration(session); !ready {
		c.registry.UnregisterSession(session.Name)
		klog.V(2).Info("Unregistered session from local registry", "reason", reason)
		return nil
	}

	// register the session with the local registry for proxying traffic
	return c.registerSession(ctx, session)
}

func (c *DataPlaneController) isReadyForRegistration(session *sessiongatev1alpha1.Session) (bool, string) {
	if !session.DeletionTimestamp.IsZero() {
		return false, "being deleted"
	}
	if !session.IsReady() {
		return false, "not ready"
	}
	return true, ""
}

func (c *DataPlaneController) getCredentialSecret(session *sessiongatev1alpha1.Session) (*CredentialSecret, error) {
	current, err := c.getSecret(session.Namespace, session.Name)
	var secretData map[string][]byte
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	} else if err != nil {
		secretData = make(map[string][]byte)
	} else {
		secretData = current.Data
	}
	return NewCredentialSecret(session.Name, session.Namespace, session.UID, c.fieldManager, secretData), nil
}

// registerSession fetches credentials and registers the session in the local registry for proxying traffic
func (c *DataPlaneController) registerSession(ctx context.Context, session *sessiongatev1alpha1.Session) error {
	credentialSecret, err := c.getCredentialSecret(session)
	if err != nil {
		return fmt.Errorf("failed to get credential secret: %w", err)
	}

	privateKeyBytes, privateKeyExists := credentialSecret.GetPrivateKeyBytes()
	if !privateKeyExists {
		return fmt.Errorf("private key doesn't exist yet")
	}
	certificate, certificateExists := credentialSecret.GetCertificate()
	if !certificateExists {
		return fmt.Errorf("certificate doesn't exist yet")
	}

	restConfig := &rest.Config{
		Host: session.Status.BackendKASURL,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: true,
			CertData: certificate,
			KeyData:  privateKeyBytes,
		},
	}

	endpoint, err := c.registry.RegisterSession(registry.NewSessionOptions(
		session.Name,
		session.Status.BackendKASURL,
		restConfig,
	))
	if err != nil {
		return fmt.Errorf("failed to register session: %w", err)
	}

	c.logger.V(2).Info("Registered session in local registry",
		"endpoint", endpoint,
		"backendKASURL", session.Status.BackendKASURL)

	return nil
}
