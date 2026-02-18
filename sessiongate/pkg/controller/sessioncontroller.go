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
	"crypto/rsa"
	"errors"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	certapplyv1 "k8s.io/client-go/applyconfigurations/certificates/v1"
	corev1applyconfigurations "k8s.io/client-go/applyconfigurations/core/v1"
	applyv1 "k8s.io/client-go/applyconfigurations/meta/v1"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"

	certificatesv1alpha1apply "github.com/openshift/hypershift/client/applyconfiguration/certificates/v1alpha1"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	sessiongatv1alpha1applyconfigurations "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/applyconfiguration/sessiongate/v1alpha1"
	sessiongateclient "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/clientset/versioned"
	sessiongateinformers "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/informers/externalversions"
)

const (
	sessionsByManagementClusterIndexName  = "sessions-by-management-cluster"
	sessionsByHostedControlPlaneIndexName = "sessions-by-hosted-control-plane"
)

func hostedControlPlaneIndexKey(mgmtClusterResourceID, hostedControlPlaneNamespace string) string {
	return fmt.Sprintf("%s/%s", mgmtClusterResourceID, hostedControlPlaneNamespace)
}

// SessionEndpointProvider provides session endpoint URLs
type SessionEndpointProvider interface {
	GetSessionEndpoint(sessionID string) string
}

type SessionController struct {
	namespace            string
	workqueue            workqueue.TypedRateLimitingInterface[cache.ObjectName]
	mgmtClusterWorkQueue workqueue.TypedRateLimitingInterface[string]
	cachesToSync         []cache.InformerSynced
	kubeClient           kubernetes.Interface
	sessiongateClient    sessiongateclient.Interface
	sessiongateInformers sessiongateinformers.SharedInformerFactory
	clock                clock.PassiveClock

	eventRecorder    record.EventRecorder
	endpointProvider SessionEndpointProvider

	mcProviders       map[string]*ManagementClusterProvider
	mcProvidersMu     sync.RWMutex
	mcProviderFactory *ManagementClusterProviderFactory

	getSession    func(namespace, name string) (*sessiongatev1alpha1.Session, error)
	getSecret     func(namespace, name string) (*corev1.Secret, error)
	newPrivateKey func(size int) (*rsa.PrivateKey, error)
}

func NewSessionController(
	kubeClient kubernetes.Interface,
	sessiongateClient sessiongateclient.Interface,
	sessiongateInformers sessiongateinformers.SharedInformerFactory,
	kubeinformers kubeinformers.SharedInformerFactory,
	eventRecorder record.EventRecorder,
	namespace string,
	managementClusterProviderFactory *ManagementClusterProviderFactory,
	endpointProvider SessionEndpointProvider,
) (*SessionController, error) {
	workQueue := workqueue.NewTypedRateLimitingQueueWithConfig(
		workqueue.DefaultTypedControllerRateLimiter[cache.ObjectName](),
		workqueue.TypedRateLimitingQueueConfig[cache.ObjectName]{
			Name: "SessionControlPlaneController",
		},
	)
	mgmtClusterWorkQueue := workqueue.NewTypedRateLimitingQueueWithConfig(
		workqueue.DefaultTypedControllerRateLimiter[string](),
		workqueue.TypedRateLimitingQueueConfig[string]{
			Name: "ManagementClusterInventory",
		},
	)

	// session informer hookup
	sessionInformer := sessiongateInformers.Sessiongate().V1alpha1().Sessions().Informer()
	if err := registerInformer(sessionInformer, keyForObject, workQueue); err != nil {
		return nil, fmt.Errorf("failed to register session informer: %w", err)
	}

	// we index sessions by management cluster resource ID so we can quickly find
	// all sessions for a given management cluster. useful for mgmt cluster informer setup.
	if err := sessionInformer.AddIndexers(cache.Indexers{
		sessionsByManagementClusterIndexName: func(obj interface{}) ([]string, error) {
			session, ok := obj.(*sessiongatev1alpha1.Session)
			if !ok {
				return nil, fmt.Errorf("object is not a Session")
			}
			return []string{session.Spec.ManagementCluster.ResourceID}, nil
		},
		sessionsByHostedControlPlaneIndexName: func(obj interface{}) ([]string, error) {
			session, ok := obj.(*sessiongatev1alpha1.Session)
			if !ok {
				return nil, fmt.Errorf("object is not a Session")
			}
			return []string{hostedControlPlaneIndexKey(session.Spec.ManagementCluster.ResourceID, session.Spec.HostedControlPlane.Namespace)}, nil
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to add indexers to session informer: %w", err)
	}
	if err := registerInformer(sessionInformer, mgmtClusterResourceIdFromSession, mgmtClusterWorkQueue); err != nil {
		return nil, fmt.Errorf("failed to register management cluster informer: %w", err)
	}

	// secret informer hookup
	secretInformer := kubeinformers.Core().V1().Secrets().Informer()
	if err := registerInformer(secretInformer, sessionKeyFromOwnershipReference, workQueue); err != nil {
		return nil, fmt.Errorf("failed to register secret informer: %w", err)
	}

	return &SessionController{
		workqueue:            workQueue,
		mgmtClusterWorkQueue: mgmtClusterWorkQueue,
		cachesToSync: []cache.InformerSynced{
			sessionInformer.HasSynced,
			secretInformer.HasSynced,
		},
		eventRecorder:        eventRecorder,
		endpointProvider:     endpointProvider,
		kubeClient:           kubeClient,
		sessiongateClient:    sessiongateClient,
		sessiongateInformers: sessiongateInformers,
		namespace:            namespace,
		clock:                clock.RealClock{},
		mcProviders:          make(map[string]*ManagementClusterProvider),
		mcProviderFactory:    managementClusterProviderFactory,
		getSession: func(namespace, name string) (*sessiongatev1alpha1.Session, error) {
			return sessiongateInformers.Sessiongate().V1alpha1().Sessions().Lister().Sessions(namespace).Get(name)
		},
		getSecret: func(namespace, name string) (*corev1.Secret, error) {
			return kubeinformers.Core().V1().Secrets().Lister().Secrets(namespace).Get(name)
		},
		newPrivateKey: createPrivateKey,
	}, nil
}

func (c *SessionController) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()
	defer c.mgmtClusterWorkQueue.ShutDown()

	klog.InfoS("Starting control plane controller... waiting for informer caches to sync")

	if ok := cache.WaitForCacheSync(ctx.Done(), c.cachesToSync...); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.InfoS("Starting workers", "count", workers)
	for range workers {
		go wait.UntilWithContext(ctx, c.runSessionWorker, time.Second)
	}

	go wait.UntilWithContext(ctx, c.runManagementClusterWorker, time.Second)

	klog.InfoS("Started workers")
	<-ctx.Done()
	klog.InfoS("Shutting down workers")

	return nil
}

func (c *SessionController) runSessionWorker(ctx context.Context) {
	for c.processNextSessionWorkItem(ctx) {
	}
}

func (c *SessionController) processNextSessionWorkItem(ctx context.Context) bool {
	objRef, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	defer c.workqueue.Done(objRef)

	session, err := c.getSession(objRef.Namespace, objRef.Name)
	if err != nil && apierrors.IsNotFound(err) {
		// session is gone, nothing to do
		return true
	} else if err != nil {
		c.workqueue.AddRateLimited(objRef)
		return true
	}

	logger := klog.FromContext(ctx).WithValues(
		"session", session.Name,
		"namespace", session.Namespace,
		"managementClusterID", session.Spec.ManagementCluster.ResourceID,
		"hostedControlPlaneResourceID", session.Spec.HostedControlPlane.Namespace,
	)
	ctx = klog.NewContext(ctx, logger)

	logger.Info("start sync")
	defer logger.Info("end sync")

	// requeue for the expiration time
	if session.Status.ExpiresAt != nil {
		requeueAfter := time.Until(session.Status.ExpiresAt.Time)
		if requeueAfter > 0 {
			c.workqueue.AddAfter(objRef, requeueAfter)
		}
	}

	// get the management cluster provider
	mc, ok := c.getManagementClusterProvider(session.Spec.ManagementCluster.ResourceID)
	if !ok {
		logger.V(4).Info(
			"management cluster provider not yet registered, skipping session reconciliation as the registration process will requeue",
		)
		return true
	}

	// reconcile the session
	err = c.syncSession(ctx, session, mc)
	if err != nil {
		utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry")
		c.workqueue.AddRateLimited(objRef)
		return true
	}
	c.workqueue.Forget(objRef)
	return true
}

func (c *SessionController) syncSession(ctx context.Context, session *sessiongatev1alpha1.Session, mc *ManagementClusterProvider) error {
	logger := klog.FromContext(ctx)

	action, err := c.processSession(ctx, session, mc)
	if err != nil {
		logger.Error(err, "Error processing session")
		return err
	}

	if action != nil {
		if err = action.validate(); err != nil {
			panic(err) // if validation fails, we have a programming error
		}
		if action.Event != nil {
			c.eventRecorder.Eventf(session, corev1.EventTypeNormal, action.Event.Reason, action.Event.MessageFmt, action.Event.Args...)
		}

		// about Force: true - we are the sole owner of these resources
		switch {
		case action.Session != nil:
			_, err = c.sessiongateClient.SessiongateV1alpha1().Sessions(session.Namespace).ApplyStatus(ctx, action.Session, metav1.ApplyOptions{FieldManager: ControllerAgentName})
		case action.Secret != nil:
			_, err = c.kubeClient.CoreV1().Secrets(*action.Secret.Namespace).Apply(ctx, action.Secret, metav1.ApplyOptions{FieldManager: ControllerAgentName, Force: true})
		case action.CSR != nil:
			_, err = mc.KubeClient.CertificatesV1().CertificateSigningRequests().Apply(ctx, action.CSR, metav1.ApplyOptions{FieldManager: ControllerAgentName, Force: true})
		case action.CSRApproval != nil:
			_, err = mc.CertificatesClient.CertificateSigningRequestApprovals(action.CSRApproval.Namespace).Apply(ctx, action.CSRApproval.Approval, metav1.ApplyOptions{FieldManager: ControllerAgentName, Force: true})
		case action.DeleteSession:
			err = c.sessiongateClient.SessiongateV1alpha1().Sessions(session.Namespace).Delete(ctx, session.Name, metav1.DeleteOptions{})
		case action.DeleteCSR:
			err = mc.KubeClient.CertificatesV1().CertificateSigningRequests().Delete(ctx, getCSRNameForSession(session), metav1.DeleteOptions{})
		}
	}

	return err
}

type actions struct {
	Event         *eventInfo
	Session       *sessiongatv1alpha1applyconfigurations.SessionApplyConfiguration
	DeleteSession bool
	Secret        *corev1applyconfigurations.SecretApplyConfiguration
	CSR           *certapplyv1.CertificateSigningRequestApplyConfiguration
	CSRApproval   *csrApprovalAction
	DeleteCSR     bool
}

type csrApprovalAction struct {
	Namespace string
	Approval  *certificatesv1alpha1apply.CertificateSigningRequestApprovalApplyConfiguration
}

func (a *actions) validate() error {
	var set int
	if a.Session != nil {
		set += 1
	}
	if a.Secret != nil {
		set += 1
	}
	if a.DeleteSession {
		set += 1
	}
	if a.CSR != nil {
		set += 1
	}
	if a.DeleteCSR {
		set += 1
	}
	if a.CSRApproval != nil {
		set += 1
	}
	if set > 1 {
		return errors.New("programmer error: more than one action set")
	}
	return nil
}

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

func (c *SessionController) processSession(ctx context.Context, session *sessiongatev1alpha1.Session, mc ManagementClusterQuerier) (*actions, error) {
	for _, step := range []sessionStep{
		// this is a new session, so we need to manifest the expiration timestamp
		c.handleExpiration,
		// verify the hosted control plane is ready
		c.verifyHostedControlPlaneReady,
		// generate credentials
		c.generateCredentials,
		// ensure network path is available
		c.ensureNetworkPath,
		// finalize session with endpoint and backend URL
		c.finalizeSession,
	} {
		// each step either handles the current step or hands off to the next one
		done, action, err := step(ctx, session, mc)
		if done {
			return action, err
		}
	}
	// nothing to do
	return nil, nil
}

// sessionStep is a step in the session reconciliation process
// returns:
// - done: whether the current reconciliation loop should stop with the current step result
// - action: the action to take
// - error: an error that occurred
type sessionStep func(ctx context.Context, session *sessiongatev1alpha1.Session, mc ManagementClusterQuerier) (bool, *actions, error)

// transient errors can be retried by requeuing with rate limiting.
func (c *SessionController) handleTransientError(err error) (bool, *actions, error) {
	return true, nil, err
}

// permanent errors usually don't resolve themselves just by retrying,
// so we set the condition and don't actively requeue by returning the error.
// we will passively though through the informer first time the permanent error
// is handled, due to the condition update and on every relist
func (c *SessionController) handlePermanentError(session *sessiongatev1alpha1.Session, condition *applyv1.ConditionApplyConfiguration) (bool, *actions, error) {
	sessionUpdate, needsUpdate := NewStatus(session.Status).
		WithConditions(
			condition,
			NotReadyCondition(session.Generation, c.clock.Now()),
		).AsApplyConfiguration(session)
	if needsUpdate {
		return true, &actions{Session: sessionUpdate}, nil
	}
	return true, nil, nil // permanent error, don't requeue
}

// retryable errors can be resolved by requeuing with rate limiting.
// we update the condition first if necessary, which cases a sync via the informer.
func (c *SessionController) handleRetryableError(session *sessiongatev1alpha1.Session, condition *applyv1.ConditionApplyConfiguration, err error) (bool, *actions, error) {
	sessionUpdate, needsUpdate := NewStatus(session.Status).
		WithConditions(
			condition,
			NotReadyCondition(session.Generation, c.clock.Now()),
		).AsApplyConfiguration(session)
	if needsUpdate {
		return true, &actions{Session: sessionUpdate}, nil
	}
	return true, nil, err
}

// handleExpiration calculates session expiration time and deletes expired sessions.
// Sets ExpiresAt on first reconcile, then checks on subsequent reconciles and deletes when TTL is exceeded.
func (c *SessionController) handleExpiration(ctx context.Context, session *sessiongatev1alpha1.Session, mc ManagementClusterQuerier) (bool, *actions, error) {
	expiresAt := metav1.NewTime(session.CreationTimestamp.Add(session.Spec.TTL.Duration))
	if c.clock.Now().After(expiresAt.Time) {
		e := event("SessionExpiration", "Session has expired, deleting %s/%s.", session.Namespace, session.Name)
		return true, &actions{Event: e, DeleteSession: true}, nil
	}
	sessionUpdate, needsUpdate := NewStatus(session.Status).
		WithExpiresAt(expiresAt).
		AsApplyConfiguration(session)
	if needsUpdate {
		return true, &actions{Session: sessionUpdate}, nil
	}
	return false, nil, nil
}

func (c *SessionController) verifyHostedControlPlaneReady(ctx context.Context, session *sessiongatev1alpha1.Session, mc ManagementClusterQuerier) (bool, *actions, error) {
	logger := klog.FromContext(ctx)
	hcp, err := mc.GetHostedControlPlane(session.Spec.HostedControlPlane.Namespace)
	if err != nil {
		switch {
		case apierrors.IsTimeout(err), apierrors.IsServerTimeout(err),
			apierrors.IsServiceUnavailable(err), apierrors.IsTooManyRequests(err):
			logger.V(4).Info("transient error retrieving HostedControlPlane", "error", err)
			return c.handleTransientError(err)
		case apierrors.IsNotFound(err):
			logger.Error(err, "failed to retrieve HostedControlPlane")
			return c.handlePermanentError(
				session,
				HostedControlPlaneNotAvailableCondition(
					sessiongatev1alpha1.HostedControlPlaneNotFoundReason,
					"HostedControlPlane not found on management cluster",
					session.Generation, c.clock.Now()),
			)
		case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
			logger.Error(err, "failed to retrieve HostedControlPlane")
			return c.handlePermanentError(
				session,
				HostedControlPlaneNotAvailableCondition(
					sessiongatev1alpha1.HostedControlPlaneAccessErrorReason,
					"Access denied to HostedControlPlane",
					session.Generation, c.clock.Now()),
			)
		default:
			logger.Error(err, "failed to retrieve HostedControlPlane")
			return c.handleRetryableError(
				session,
				HostedControlPlaneNotAvailableCondition(sessiongatev1alpha1.HostedControlPlaneAccessErrorReason,
					"Unable to access HostedControlPlane in management cluster",
					session.Generation, c.clock.Now()),
				err)
		}
	}

	hcpAvailable := meta.FindStatusCondition(hcp.Status.Conditions, "Available")
	if hcpAvailable == nil || hcpAvailable.Status != metav1.ConditionTrue {
		sessionUpdate, needsUpdate := NewStatus(session.Status).
			WithConditions(
				HostedControlPlaneNotAvailableCondition(sessiongatev1alpha1.HostedControlPlaneNotReadyReason, "HostedControlPlane exists but is not ready", session.Generation, c.clock.Now()),
				NotReadyCondition(session.Generation, c.clock.Now()),
			).AsApplyConfiguration(session)
		if needsUpdate {
			return true, &actions{Session: sessionUpdate}, nil
		}
		return true, nil, nil // don't requeue if HCP is not ready, the informer will let us know when it changes
	}

	// HCP exists and is available, set condition to true
	sessionUpdate, needsUpdate := NewStatus(session.Status).
		WithConditions(
			HostedControlPlaneAvailableCondition(session.Generation, c.clock.Now()),
		).AsApplyConfiguration(session)
	if needsUpdate {
		return true, &actions{Session: sessionUpdate}, nil
	}
	return false, nil, nil
}

func (c *SessionController) getCredentialSecret(session *sessiongatev1alpha1.Session) (*CredentialSecret, error) {
	existingSecret, err := c.getSecret(session.Namespace, credentialSecretNameForSession(session))
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	return NewCredentialSecret(existingSecret), nil
}

func (c *SessionController) generateCredentials(ctx context.Context, session *sessiongatev1alpha1.Session, mc ManagementClusterQuerier) (bool, *actions, error) {
	logger := klog.FromContext(ctx)
	credentialSecret, err := c.getCredentialSecret(session)
	if err != nil {
		switch {
		case apierrors.IsTimeout(err), apierrors.IsServerTimeout(err),
			apierrors.IsServiceUnavailable(err), apierrors.IsTooManyRequests(err):
			logger.V(4).Info("transient error retrieving credential secret", "error", err)
			return c.handleTransientError(err)
		case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
			logger.Error(err, "failed to retrieve credential secret")
			return c.handlePermanentError(
				session,
				CredentialsNotAvailableCondition(sessiongatev1alpha1.CredentialsSecretAccessErrorReason,
					"Access denied when retrieving credential secret",
					session.Generation, c.clock.Now()),
			)
		default:
			logger.Error(err, "failed to retrieve credential secret")
			return c.handleRetryableError(
				session,
				CredentialsNotAvailableCondition(sessiongatev1alpha1.CredentialsSecretAccessErrorReason,
					"Unable to retrieve credential secret",
					session.Generation, c.clock.Now()),
				err,
			)
		}
	}

	// if there is already a certificate in the secret, nothing to do
	if _, certExists := credentialSecret.GetCertificate(); certExists {
		sessionUpdate, needsUpdate := NewStatus(session.Status).
			WithConditions(
				applyv1.Condition().
					WithType(string(sessiongatev1alpha1.SessionConditionTypeCredentialsAvailable)).
					WithStatus(metav1.ConditionTrue).
					WithReason(sessiongatev1alpha1.CredentialsAvailableReason).
					WithMessage("Credentials available").
					WithObservedGeneration(session.Generation).
					WithLastTransitionTime(metav1.NewTime(c.clock.Now())),
			).AsApplyConfiguration(session)
		if needsUpdate {
			return true, &actions{Session: sessionUpdate}, nil
		}
		return false, nil, nil
	}

	// the certificate is not yet in the secret, so lets check the CSR and update the secret
	csrName := getCSRNameForSession(session)
	csr, err := mc.GetCSR(csrName)
	if err != nil && !apierrors.IsNotFound(err) {
		switch {
		case apierrors.IsTimeout(err), apierrors.IsServerTimeout(err),
			apierrors.IsServiceUnavailable(err), apierrors.IsTooManyRequests(err):
			logger.V(4).Info("transient error retrieving CSR", "error", err)
			return c.handleTransientError(err)
		case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
			logger.Error(err, "failed to retrieve CSR", "csr", csrName)
			return c.handlePermanentError(
				session,
				CredentialsNotAvailableCondition(sessiongatev1alpha1.CertificateSigningRequestAccessErrorReason,
					"Access denied when retrieving certificate signing request",
					session.Generation, c.clock.Now()),
			)
		default:
			logger.Error(err, "failed to retrieve CSR", "csr", csrName)
			return c.handleRetryableError(
				session,
				CredentialsNotAvailableCondition(sessiongatev1alpha1.CertificateSigningRequestAccessErrorReason,
					"Unable to retrieve certificate signing request from management cluster",
					session.Generation, c.clock.Now()),
				err)
		}
	}

	// a CSR exists
	if csr != nil {
		// ... but it's invalid, so we need to delete it and regenerate
		privateKey, privateKeyExists := credentialSecret.GetPrivateKey()
		if !privateKeyExists || !validateCSR(csr, privateKey, session.Spec.Owner.Name, session.Spec.AccessLevel.Group) {
			e := event("CSRInvalid", "CSR for %s/%s is invalid, deleting to regenerate.", session.Namespace, session.Name)
			return true, &actions{Event: e, DeleteCSR: true}, nil
		}
		// ... if it has a certificate, we can bring it to the secret
		if len(csr.Status.Certificate) > 0 {
			return true, &actions{Secret: credentialSecret.ApplyConfigurationForCertificate(session, csr.Status.Certificate)}, nil
		}
		// ... if it is approved but has no certificate yet, we just need to wait
		// the informer will let us know when the CSR changes
		if len(csr.Status.Certificate) == 0 && isCSRApproved(csr) {
			return true, nil, nil
		}
		// ... if not, let's handle approval
		if !isCSRApproved(csr) {
			sessionUpdate, needsUpdate := NewStatus(session.Status).
				WithConditions(
					CredentialsNotAvailableCondition(sessiongatev1alpha1.CertificateSigningRequestPendingReason, "Certificate signing request pending, waiting for approval", session.Generation, c.clock.Now()),
					NotReadyCondition(session.Generation, c.clock.Now()),
				).AsApplyConfiguration(session)
			if needsUpdate {
				return true, &actions{Session: sessionUpdate}, nil
			}

			csrApproval, err := mc.GetCSRApproval(session.Spec.HostedControlPlane.Namespace, csrName)
			if err != nil && !apierrors.IsNotFound(err) {
				switch {
				case apierrors.IsTimeout(err), apierrors.IsServerTimeout(err),
					apierrors.IsServiceUnavailable(err), apierrors.IsTooManyRequests(err):
					logger.V(4).Info("transient error retrieving CSR approval", "error", err)
					return c.handleTransientError(err)
				case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
					logger.Error(err, "failed to retrieve CSR approval", "csrApproval", csrName)
					return c.handlePermanentError(
						session,
						CredentialsNotAvailableCondition(sessiongatev1alpha1.CertificateSigningRequestAccessErrorReason,
							"Access denied when retrieving certificate signing request approval",
							session.Generation, c.clock.Now()),
					)
				default:
					logger.Error(err, "failed to retrieve CSR approval", "csrApproval", csrName)
					return c.handleRetryableError(
						session,
						CredentialsNotAvailableCondition(sessiongatev1alpha1.CertificateSigningRequestAccessErrorReason,
							"Unable to retrieve certificate signing request approval from management cluster",
							session.Generation, c.clock.Now()),
						err)
				}
			}
			if csrApproval == nil {
				return true, &actions{CSRApproval: &csrApprovalAction{
					Namespace: session.Spec.HostedControlPlane.Namespace,
					Approval: certificatesv1alpha1apply.CertificateSigningRequestApproval(
						csrName,
						session.Spec.HostedControlPlane.Namespace,
					),
				}}, nil
			}
			// approval is in place, so we just need to wait some more - the CSR informer will let us know when the CSR changed
			return true, nil, nil
		}
	}

	// there is no CSR yet, so we need to create one
	privateKey, privateKeyExists := credentialSecret.GetPrivateKey()
	if privateKeyExists {
		sessionUpdate, needsUpdate := NewStatus(session.Status).
			WithConditions(
				CredentialsNotAvailableCondition("PrivateKeyCreated", "Private key created, waiting for CSR to be created", session.Generation, c.clock.Now()),
				NotReadyCondition(session.Generation, c.clock.Now()),
			).AsApplyConfiguration(session)
		if needsUpdate {
			return true, &actions{Session: sessionUpdate}, nil
		}

		csrApplyConfig, err := createCSRApplyConfiguration(session, privateKey)
		if err != nil {
			logger.Error(err, "failed to prepare CSR apply configuration")
			return c.handlePermanentError(
				session,
				CredentialsNotAvailableCondition(sessiongatev1alpha1.CertificateSigningRequestCreationFailedReason,
					"Failed to prepare certificate signing request",
					session.Generation, c.clock.Now()),
			)
		}
		e := event("CSRGeneration", "Creating CSR for %s/%s on management cluster.", session.Namespace, session.Name)
		return true, &actions{Event: e, CSR: csrApplyConfig}, nil
	}

	// ... but to create a CSR, we need a private key first
	if session.Status.CredentialsSecretRef != "" {
		privateKey, err = c.newPrivateKey(RSAKeySize)
		if err != nil {
			logger.Error(err, "failed to generate private key")
			return c.handleRetryableError(
				session,
				CredentialsNotAvailableCondition(sessiongatev1alpha1.PrivateKeyGenerationFailedReason,
					"Private key generation failed",
					session.Generation, c.clock.Now()),
				err,
			)
		}
		e := event("PrivateKeyGeneration", "Generating private key for %s/%s.", session.Namespace, session.Name)
		return true, &actions{Event: e, Secret: credentialSecret.ApplyConfigurationForPrivateKey(session, privateKey)}, nil
	}

	// ... but before we can create a private key, we need to write down the secret name in the status
	sessionUpdate, _ := NewStatus(session.Status).
		WithCredentialsSecretRef(credentialSecretNameForSession(session)).
		AsApplyConfiguration(session)
	return true, &actions{Session: sessionUpdate}, nil
}

func (c *SessionController) ensureNetworkPath(ctx context.Context, session *sessiongatev1alpha1.Session, mc ManagementClusterQuerier) (bool, *actions, error) {
	// right now we only have public HCPs, so we just use the public HCP API endpoint as
	// network path and set it in the status
	// once we have private HCPs, this step will establish network connectivity
	// (e.g. portforwarding to the HCP's API server pods) and publish the in-cluster
	// endpoint in the status
	if session.Status.BackendKASURL != "" {
		return false, nil, nil
	}

	hcp, err := mc.GetHostedControlPlane(session.Spec.HostedControlPlane.Namespace)
	if err != nil {
		// we don't treat errors here but requeue because the verifyHostedControlPlaneReady
		// step at the beginning of the chain will handle them appropriately and requeue if needed
		return true, nil, err
	}

	statusUpdate, needsUpdate := NewStatus(session.Status).
		WithBackendKASURL(fmt.Sprintf("https://%s", hcp.Spec.KubeAPIServerDNSName)).
		WithConditions(
			applyv1.Condition().
				WithType(string(sessiongatev1alpha1.SessionConditionTypeNetworkPathAvailable)).
				WithStatus(metav1.ConditionTrue).
				WithReason(sessiongatev1alpha1.NetworkPathAvailableReason).
				WithMessage("Network path available via public endpoint").
				WithObservedGeneration(session.Generation).
				WithLastTransitionTime(metav1.NewTime(c.clock.Now())),
		).
		AsApplyConfiguration(session)
	if needsUpdate {
		e := event("NetworkPathAvailable", "Network path available via public endpoint for session %s/%s.", session.Namespace, session.Name)
		return true, &actions{Event: e, Session: statusUpdate}, nil
	}
	return false, nil, nil
}

func (c *SessionController) finalizeSession(ctx context.Context, session *sessiongatev1alpha1.Session, mc ManagementClusterQuerier) (bool, *actions, error) {
	// build status update
	statusUpdate, needsUpdate := NewStatus(session.Status).
		WithEndpoint(c.endpointProvider.GetSessionEndpoint(session.Name)).
		WithConditions(
			applyv1.Condition().
				WithType(string(sessiongatev1alpha1.SessionConditionTypeReady)).
				WithStatus(metav1.ConditionTrue).
				WithReason(sessiongatev1alpha1.SessionReadyReason).
				WithMessage("Session is ready").
				WithObservedGeneration(session.Generation).
				WithLastTransitionTime(metav1.NewTime(c.clock.Now())),
		).
		AsApplyConfiguration(session)
	if needsUpdate {
		e := event("SessionFinalization", "Finalizing session %s/%s with endpoint and backend URL.", session.Namespace, session.Name)
		return true, &actions{Event: e, Session: statusUpdate}, nil
	}
	return false, nil, nil
}

func (c *SessionController) runManagementClusterWorker(ctx context.Context) {
	for c.processNextManagementClusterWorkItem(ctx) {
	}
}

func (c *SessionController) processNextManagementClusterWorkItem(ctx context.Context) bool {
	mgmtClusterResourceID, shutdown := c.mgmtClusterWorkQueue.Get()
	if shutdown {
		return false
	}
	defer c.mgmtClusterWorkQueue.Done(mgmtClusterResourceID)

	if err := c.reconcileManagementClusterProvider(ctx, mgmtClusterResourceID); err != nil {
		c.mgmtClusterWorkQueue.AddRateLimited(mgmtClusterResourceID)
		return true
	}

	c.mgmtClusterWorkQueue.Forget(mgmtClusterResourceID)
	return true
}

func (c *SessionController) reconcileManagementClusterProvider(ctx context.Context, mgmtClusterID string) error {
	sessions, err := c.getSessionsByManagementCluster(mgmtClusterID)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		return c.unregisterMCProvider(mgmtClusterID)
	} else {
		return c.registerMCProvider(ctx, mgmtClusterID, MCProviderCacheSyncTimeout)
	}
}

func (c *SessionController) getSessionsByManagementCluster(mgmtClusterResourceID string) ([]*sessiongatev1alpha1.Session, error) {
	objs, err := c.sessiongateInformers.Sessiongate().V1alpha1().Sessions().Informer().GetIndexer().ByIndex(
		sessionsByManagementClusterIndexName,
		mgmtClusterResourceID,
	)
	if err != nil {
		return nil, err
	}
	sessions := make([]*sessiongatev1alpha1.Session, len(objs))
	for i, obj := range objs {
		sessions[i] = obj.(*sessiongatev1alpha1.Session)
	}
	return sessions, nil
}
