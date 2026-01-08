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

	sessiongateinformers "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/informers/externalversions"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/controller"
	corev1 "k8s.io/api/core/v1"
)

// data plane controller implementation.
// it runs on all replicas and registers sessions with the proxy registry, so that any replica can
// proxy traffic for a session.
type Controller struct {
	fieldManager string
	logger       klog.Logger
	registry     controller.SessionRegistry
	getSession   func(namespace, name string) (*sessiongatev1alpha1.Session, error)
	getSecret    func(namespace, name string) (*corev1.Secret, error)
}

func NewController(
	ctx context.Context,
	logger klog.Logger,
	sessiongateInformers sessiongateinformers.SharedInformerFactory,
	kubeInformers kubeinformers.SharedInformerFactory,
	registry controller.SessionRegistry,
	eventRecorder events.Recorder,
) factory.Controller {
	c := &Controller{
		fieldManager: controller.ControllerAgentName,
		logger:       logger,
		registry:     registry,
		getSession: func(namespace, name string) (*sessiongatev1alpha1.Session, error) {
			return sessiongateInformers.Sessiongate().V1alpha1().Sessions().Lister().Sessions(namespace).Get(name)
		},
		getSecret: func(namespace, name string) (*corev1.Secret, error) {
			return kubeInformers.Core().V1().Secrets().Lister().Secrets(namespace).Get(name)
		},
	}

	return factory.New().
		WithInformersQueueKeysFunc(enqueueSession, sessiongateInformers.Sessiongate().V1alpha1().Sessions().Informer()).
		WithInformersQueueKeysFunc(
			controller.EnqueueOwningSession,
			kubeInformers.Core().V1().Secrets().Informer(),
		).
		WithSync(c.syncSession).
		ResyncEvery(time.Minute*5).
		ToController("SessionDataPlaneController", eventRecorder.WithComponentSuffix(c.fieldManager))
}

func enqueueSession(obj runtime.Object) []string {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		klog.ErrorS(err, "could not determine queue key")
		return nil
	}
	return []string{key}
}

func (c *Controller) syncSession(ctx context.Context, syncContext factory.SyncContext) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(syncContext.QueueKey())
	if err != nil {
		return err
	}

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

func (c *Controller) getCredentialSecret(ctx context.Context, session *sessiongatev1alpha1.Session) (*controller.CredentialSecret, error) {
	current, err := c.getSecret(session.Namespace, session.Name)
	var secretData map[string][]byte
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	} else if err != nil {
		secretData = make(map[string][]byte)
	} else {
		secretData = current.Data
	}
	return controller.NewCredentialSecret(session.Name, session.Namespace, session.UID, c.fieldManager, secretData), nil
}

// registerSession fetches credentials and registers the session in the local registry for proxying traffic
func (c *Controller) registerSession(ctx context.Context, session *sessiongatev1alpha1.Session) error {
	credentialSecret, err := c.getCredentialSecret(ctx, session)
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

	endpoint, err := c.registry.RegisterSession(controller.NewSessionOptions(
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
