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

// This is the data plane controller, reacting to sessions becoming ready and offering it to the proxy registry.
// Also reacts to sessions being deleted and unregisters them from the proxy registry.
type DataplaneController struct {
	workqueue    workqueue.TypedRateLimitingInterface[cache.ObjectName]
	cachesToSync []cache.InformerSynced
	logger       klog.Logger
	registry     registry.SessionRegistry
	getSession   func(namespace, name string) (*sessiongatev1alpha1.Session, error)
	getSecret    func(namespace, name string) (*corev1.Secret, error)
}

func NewDataplaneController(
	ctx context.Context,
	logger klog.Logger,
	sessiongateInformers sessiongateinformers.SharedInformerFactory,
	kubeInformers kubeinformers.SharedInformerFactory,
	registry registry.SessionRegistry,
	eventRecorder record.EventRecorder,
) (*DataplaneController, error) {
	workQueue := workqueue.NewTypedRateLimitingQueueWithConfig(
		workqueue.DefaultTypedControllerRateLimiter[cache.ObjectName](),
		workqueue.TypedRateLimitingQueueConfig[cache.ObjectName]{
			Name: "DataplaneController",
		},
	)

	// session informer hookup
	sessionInformer := sessiongateInformers.Sessiongate().V1alpha1().Sessions().Informer()
	if err := registerInformer(sessionInformer, keyForObject, workQueue); err != nil {
		return nil, fmt.Errorf("failed to register session informer: %w", err)
	}

	// secret informer hookup
	secretInformer := kubeInformers.Core().V1().Secrets().Informer()
	if err := registerInformer(secretInformer, sessionKeyFromOwnershipReference, workQueue); err != nil {
		return nil, fmt.Errorf("failed to register secret informer: %w", err)
	}

	return &DataplaneController{
		workqueue: workQueue,
		cachesToSync: []cache.InformerSynced{
			sessionInformer.HasSynced,
			secretInformer.HasSynced,
		},
		logger:   logger,
		registry: registry,
		getSession: func(namespace, name string) (*sessiongatev1alpha1.Session, error) {
			return sessiongateInformers.Sessiongate().V1alpha1().Sessions().Lister().Sessions(namespace).Get(name)
		},
		getSecret: func(namespace, name string) (*corev1.Secret, error) {
			return kubeInformers.Core().V1().Secrets().Lister().Secrets(namespace).Get(name)
		},
	}, nil
}

func (c *DataplaneController) Run(ctx context.Context, workers int) error {
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

func (c *DataplaneController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *DataplaneController) processNextWorkItem(ctx context.Context) bool {
	objRef, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	defer c.workqueue.Done(objRef)
	// reconcile the session
	err := c.syncSession(ctx, objRef)
	if err != nil {
		utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry")
		c.workqueue.AddRateLimited(objRef)
		return true
	}
	c.workqueue.Forget(objRef)
	return true
}

func (c *DataplaneController) syncSession(ctx context.Context, objRef cache.ObjectName) error {
	logger := klog.FromContext(ctx).WithValues(
		"session", objRef.Name,
		"namespace", objRef.Namespace,
	)
	logger.Info("start sync")
	defer logger.Info("end sync")

	session, err := c.getSession(objRef.Namespace, objRef.Name)
	if err != nil && apierrors.IsNotFound(err) {
		// session is gone, unregister it from the proxy
		logger.Info("session is gone, unregistering from proxy")
		c.registry.UnregisterSession(objRef.Name)
		return nil
	} else if err != nil {
		return nil
	}

	if ready, _ := c.isReadyForRegistration(session); !ready {
		logger.Info("session is not ready, unregistering from proxy")
		c.registry.UnregisterSession(session.Name)
		return nil
	}

	// register the session with the local registry for proxying traffic
	logger.Info("registering session with proxy")
	return c.registerSession(session)
}

func (c *DataplaneController) isReadyForRegistration(session *sessiongatev1alpha1.Session) (bool, string) {
	if !session.DeletionTimestamp.IsZero() {
		return false, "being deleted"
	}
	if !session.IsReady() {
		return false, "not ready"
	}
	return true, ""
}

func (c *DataplaneController) getCredentialSecret(session *sessiongatev1alpha1.Session) (*CredentialSecret, error) {
	existingSecret, err := c.getSecret(session.Namespace, credentialSecretNameForSession(session))
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	return NewCredentialSecret(existingSecret), nil
}

func (c *DataplaneController) registerSession(session *sessiongatev1alpha1.Session) error {
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

	_, err = c.registry.RegisterSession(session.Name, session.Spec.HostedControlPlane.ResourceID, session.Spec.Owner, restConfig)
	if err != nil {
		return fmt.Errorf("failed to register session: %w", err)
	}

	return nil
}
