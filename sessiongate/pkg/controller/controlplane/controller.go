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

package controlplane

import (
	"context"
	"reflect"
	"runtime"
	"sync"

	certificatesv1 "k8s.io/api/certificates/v1"
	applyv1 "k8s.io/client-go/applyconfigurations/meta/v1"

	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/openshift/library-go/pkg/operator/events"
	"google.golang.org/protobuf/proto"
	securityv1beta1api "istio.io/api/security/v1beta1"
	typev1beta1 "istio.io/api/type/v1beta1"
	securityv1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	metaapplyv1 "istio.io/client-go/pkg/applyconfiguration/meta/v1"
	securityapplyv1beta1 "istio.io/client-go/pkg/applyconfiguration/security/v1beta1"
	istioclient "istio.io/client-go/pkg/clientset/versioned/typed/security/v1beta1"
	istioinformers "istio.io/client-go/pkg/informers/externalversions"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"k8s.io/apimachinery/pkg/util/wait"
	corev1applyconfigurations "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/workqueue"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/controller"
	sessiongatv1alpha1applyconfigurations "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/applyconfiguration/sessiongate/v1alpha1"
	sessiongateclient "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/clientset/versioned"
	sessiongateinformers "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/informers/externalversions"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/mc"

	corev1 "k8s.io/api/core/v1"
	certapplyv1 "k8s.io/client-go/applyconfigurations/certificates/v1"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	certificatesv1alpha1apply "github.com/openshift/hypershift/client/applyconfiguration/certificates/v1alpha1"
)

// SessionEndpointProvider provides session endpoint URLs
type SessionEndpointProvider interface {
	GetSessionEndpoint(sessionID string) string
}

type SessionController struct {
	workqueue         workqueue.TypedRateLimitingInterface[cache.ObjectName]
	cachesToSync      []cache.InformerSynced
	kubeClient        kubernetes.Interface
	sessiongateClient sessiongateclient.Interface
	istioClient       istioclient.SecurityV1beta1Interface

	fieldManager                 string
	endpointProvider             SessionEndpointProvider
	getSession                   func(namespace, name string) (*sessiongatev1alpha1.Session, error)
	getAuthorizationPolicy       func(namespace, name string) (*securityv1beta1.AuthorizationPolicy, error)
	getSecret                    func(namespace, name string) (*corev1.Secret, error)
	getManagementClusterProvider func(ctx context.Context, resourceID string) (*mc.ManagementClusterProvider, error)
	newPrivateKey                func(size int) (*rsa.PrivateKey, error)
}

func NewSessionController(
	kubeClient kubernetes.Interface,
	sessiongateClient sessiongateclient.Interface,
	istioClient istioclient.SecurityV1beta1Interface,
	sessiongateInformers sessiongateinformers.SharedInformerFactory,
	istioInformers istioinformers.SharedInformerFactory,
	kubeinformers kubeinformers.SharedInformerFactory,
	eventRecorder events.Recorder,
	managementClusterProviderBuilder mc.ManagementClusterProviderBuilder,
	endpointProvider SessionEndpointProvider,
) (*SessionController, error) {
	managementClusterProviders := make(map[string]*mc.ManagementClusterProvider)
	managementClusterProviderMutex := sync.Mutex{}
	workQueue := workqueue.NewTypedRateLimitingQueueWithConfig(
		workqueue.DefaultTypedControllerRateLimiter[cache.ObjectName](),
		workqueue.TypedRateLimitingQueueConfig[cache.ObjectName]{
			Name: "SessionControlPlaneController",
		},
	)
	c := &SessionController{
		workqueue:         workQueue,
		cachesToSync:      []cache.InformerSynced{},
		fieldManager:      controller.ControllerAgentName,
		endpointProvider:  endpointProvider,
		kubeClient:        kubeClient,
		sessiongateClient: sessiongateClient,
		istioClient:       istioClient,
		getSession: func(namespace, name string) (*sessiongatev1alpha1.Session, error) {
			return sessiongateInformers.Sessiongate().V1alpha1().Sessions().Lister().Sessions(namespace).Get(name)
		},
		getAuthorizationPolicy: func(namespace, name string) (*securityv1beta1.AuthorizationPolicy, error) {
			return istioInformers.Security().V1beta1().AuthorizationPolicies().Lister().AuthorizationPolicies(namespace).Get(name)
		},
		getSecret: func(namespace, name string) (*corev1.Secret, error) {
			return kubeinformers.Core().V1().Secrets().Lister().Secrets(namespace).Get(name)
		},
		getManagementClusterProvider: func(ctx context.Context, resourceID string) (*mc.ManagementClusterProvider, error) {
			managementClusterProviderMutex.Lock()
			defer managementClusterProviderMutex.Unlock()
			if _, ok := managementClusterProviders[resourceID]; !ok {
				klog.InfoS("building management cluster provider", "resourceID", resourceID)
				managementClusterProvider, err := managementClusterProviderBuilder(ctx, resourceID)
				if err != nil {
					return nil, err
				}
				managementClusterProviders[resourceID] = managementClusterProvider
				klog.InfoS("starting management cluster provider informers", "resourceID", resourceID)
				klog.InfoS("registering management cluster provider informer with work queue", "resourceID", resourceID)
				informer := managementClusterProvider.KubeInformers.Certificates().V1().CertificateSigningRequests().Informer()
				err = registerInformer(informer, keyForOwningSession, workQueue)
				if err != nil {
					return nil, err
				}
				managementClusterProvider.KubeInformers.Start(ctx.Done())
				klog.InfoS("waiting for management cluster provider cache to sync", "resourceID", resourceID)

				// Wait in a separate goroutine with timeout
				syncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()

				synced := make(chan bool, 1)
				go func() {
					synced <- cache.WaitForCacheSync(syncCtx.Done(), informer.HasSynced)
				}()

				select {
				case ok := <-synced:
					if !ok {
						return nil, fmt.Errorf("failed to wait for caches to sync")
					}
				case <-syncCtx.Done():
					return nil, fmt.Errorf("timeout waiting for cache to sync")
				}

				klog.InfoS("management cluster provider cache synced", "resourceID", resourceID)
			}
			return managementClusterProviders[resourceID], nil
		},
		newPrivateKey: func(size int) (*rsa.PrivateKey, error) {
			return rsa.GenerateKey(rand.Reader, size)
		},
	}

	// Register main informer
	if err := registerInformer(sessiongateInformers.Sessiongate().V1alpha1().Sessions().Informer(), keyForSession, c.workqueue); err != nil {
		return nil, fmt.Errorf("failed to register session informer: %w", err)
	}
	// Register secondary informers
	if err := registerInformer(istioInformers.Security().V1beta1().AuthorizationPolicies().Informer(), keyForOwningSession, c.workqueue); err != nil {
		return nil, fmt.Errorf("failed to register authorization policy informer: %w", err)
	}
	if err := registerInformer(kubeinformers.Core().V1().Secrets().Informer(), keyForOwningSession, c.workqueue); err != nil {
		return nil, fmt.Errorf("failed to register secret informer: %w", err)
	}

	return c, nil
}

func registerInformer(informer cache.SharedIndexInformer, keyFunc func(obj interface{}) (cache.ObjectName, error), workQueue workqueue.TypedRateLimitingInterface[cache.ObjectName]) error {
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := keyFunc(obj)
			if err != nil {
				return
			}
			workQueue.Add(key)
		},
		UpdateFunc: func(old, new interface{}) {
			key, err := keyFunc(new)
			if err != nil {
				return
			}
			workQueue.Add(key)
		},
		DeleteFunc: func(obj interface{}) {
			key, err := keyFunc(obj)
			if err != nil {
				return
			}
			workQueue.Add(key)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to add event handler for informer: %w", err)
	}
	return nil
}

func keyForSession(obj interface{}) (cache.ObjectName, error) {
	key, err := cache.DeletionHandlingObjectToName(obj)
	if err != nil {
		return cache.ObjectName{}, fmt.Errorf("could not determine queue key: %w", err)
	}
	return key, nil
}

func keyForOwningSession(obj interface{}) (cache.ObjectName, error) {
	var object metav1.Object
	var ok bool
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			return cache.ObjectName{}, fmt.Errorf("error decoding object, invalid type")
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			return cache.ObjectName{}, fmt.Errorf("error decoding object tombstone, invalid type")
		}
	}
	if ownerRef := metav1.GetControllerOf(object); ownerRef != nil {
		if ownerRef.Kind != "Session" {
			return cache.ObjectName{}, fmt.Errorf("object is not owned by a Session")
		}
		return cache.NewObjectName(object.GetNamespace(), ownerRef.Name), nil
	}
	if sessiongateAnnotation, ok := object.GetAnnotations()[controller.AnnotationSessiongate]; ok {
		namespace, name, err := cache.SplitMetaNamespaceKey(sessiongateAnnotation)
		if err != nil {
			return cache.ObjectName{}, fmt.Errorf("failed to split meta namespace key: %w", err)
		}
		return cache.NewObjectName(namespace, name), nil
	}
	return cache.ObjectName{}, fmt.Errorf("object has no controller owner reference")
}

func (c *SessionController) Run(ctx context.Context, workers int) error {
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

// runWorker continually calls processNextWorkItem to read and process messages on the workqueue
func (c *SessionController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem reads a single work item off the workqueue and attempts to process it
func (c *SessionController) processNextWorkItem(ctx context.Context) bool {
	objRef, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	defer c.workqueue.Done(objRef)

	requeueAfter, err := c.syncSession(ctx, objRef.Namespace, objRef.Name)
	if err == nil {
		c.workqueue.Forget(objRef)

		if requeueAfter > 0 {
			c.workqueue.AddAfter(objRef, requeueAfter)
		}
		return true
	}
	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", objRef)
	c.workqueue.AddRateLimited(objRef)
	return true
}

func (c *SessionController) syncSession(ctx context.Context, namespace, name string) (time.Duration, error) {
	session, err := c.getSession(namespace, name)
	if err != nil && apierrors.IsNotFound(err) {
		return 0, nil // nothing to be done, Session is gone
	} else if err != nil {
		return 0, err
	}

	mc, err := c.getManagementClusterProvider(ctx, session.Spec.ManagementCluster.ResourceID)
	if err != nil {
		return 0, err
	}

	action, requeueAfter, err := c.processSession(ctx, session, mc, nil)
	if err != nil {
		klog.ErrorS(err, "Error processing session", "session", session.Name, "namespace", namespace)
		return 0, err
	}

	if action != nil {
		if err = action.validate(); err != nil {
			panic(err) // if validation fails, we have a programming error
		}
		/*if action.event != nil {
			syncContext.Recorder().Eventf(action.event.reason, action.event.messageFmt, action.event.args...)
		}*/

		switch {
		case action.session != nil:
			_, err = c.sessiongateClient.SessiongateV1alpha1().Sessions(namespace).ApplyStatus(ctx, action.session, metav1.ApplyOptions{FieldManager: c.fieldManager})
		case action.secret != nil:
			_, err = c.kubeClient.CoreV1().Secrets(*action.secret.Namespace).Apply(ctx, action.secret, metav1.ApplyOptions{FieldManager: c.fieldManager, Force: true})
		case action.authPolicy != nil:
			_, err = c.istioClient.AuthorizationPolicies(*action.authPolicy.Namespace).Apply(ctx, action.authPolicy, metav1.ApplyOptions{FieldManager: c.fieldManager, Force: true})
		case action.csr != nil:
			_, err = mc.KubeClient.CertificatesV1().CertificateSigningRequests().Apply(ctx, action.csr, metav1.ApplyOptions{FieldManager: c.fieldManager, Force: true})
		case action.csrApproval != nil:
			_, err = mc.CertificatesClient.CertificateSigningRequestApprovals(action.csrApproval.namespace).Apply(ctx, action.csrApproval.approval, metav1.ApplyOptions{FieldManager: c.fieldManager, Force: true})
		case action.deleteSession:
			err = c.sessiongateClient.SessiongateV1alpha1().Sessions(namespace).Delete(ctx, name, metav1.DeleteOptions{})
		case action.deleteCSR:
			err = mc.KubeClient.CertificatesV1().CertificateSigningRequests().Delete(ctx, CSRName(session.Name), metav1.DeleteOptions{})
		}
	}

	return requeueAfter, err
}

type actions struct {
	event         *eventInfo
	session       *sessiongatv1alpha1applyconfigurations.SessionApplyConfiguration
	deleteSession bool
	secret        *corev1applyconfigurations.SecretApplyConfiguration
	authPolicy    *securityapplyv1beta1.AuthorizationPolicyApplyConfiguration
	csr           *certapplyv1.CertificateSigningRequestApplyConfiguration
	csrApproval   *csrApprovalAction
	deleteCSR     bool
}

type csrApprovalAction struct {
	namespace string
	approval  *certificatesv1alpha1apply.CertificateSigningRequestApprovalApplyConfiguration
}

func (a *actions) validate() error {
	var set int
	if a.session != nil {
		set += 1
	}
	if a.authPolicy != nil {
		set += 1
	}
	if a.secret != nil {
		set += 1
	}
	if a.deleteSession {
		set += 1
	}
	if a.csr != nil {
		set += 1
	}
	if a.deleteCSR {
		set += 1
	}
	if a.csrApproval != nil {
		set += 1
	}
	if set > 1 {
		return errors.New("programmer error: more than one action set")
	}
	return nil
}

type eventInfo struct {
	reason, messageFmt string
	args               []interface{}
}

func event(reason, messageFmt string, args ...interface{}) *eventInfo {
	return &eventInfo{
		reason:     reason,
		messageFmt: messageFmt,
		args:       args,
	}
}

func (c *SessionController) processSession(ctx context.Context, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier, now func() time.Time) (*actions, time.Duration, error) {
	if now == nil {
		now = time.Now
	}

	for _, step := range []sessionStep{
		// this is a new session, so we need to manifest the expiration timestamp
		c.handleExpiration,
		// generate an authorization policy for the future session
		c.generateAuthorizationPolicy,
		// generate credentials
		c.generateCredentials,
		// finalize session with endpoint and backend URL
		c.finalizeSession,
	} {
		// each step either handles the current step or hands off to the next one
		done, action, requeue, err := step(ctx, now, session, mc)
		if err != nil {
			klog.ErrorS(err, "Step error", "step", reflect.TypeOf(step).Name(), "err", err)
		}
		if done {
			if action != nil {
				klog.InfoS("Step done", "stepFunctionName", runtime.FuncForPC(reflect.ValueOf(step).Pointer()).Name(), "action", *action, "requeue", requeue, "err", err)
			} else {
				klog.InfoS("Step done", "stepFunctionName", runtime.FuncForPC(reflect.ValueOf(step).Pointer()).Name(), "requeue", requeue, "err", err)
			}
			return action, requeue, err
		}
	}
	// nothing to do
	return nil, 0, nil
}

// sessionStep is a step in the session reconciliation process
// returns:
// - done: whether the current reconciliation loop should stop with the current step result
// - action: the action to take
// - requeue: when to requeue the session
// - error: an error that occurred
type sessionStep func(ctx context.Context, now func() time.Time, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier) (bool, *actions, time.Duration, error)

func (c *SessionController) handleExpiration(ctx context.Context, now func() time.Time, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier) (bool, *actions, time.Duration, error) {
	expiresAt := metav1.NewTime(session.CreationTimestamp.Add(session.Spec.TTL.Duration))
	if now().After(expiresAt.Time) {
		e := event("SessionExpiration", "Session has expired, deleting %s/%s.", session.Namespace, session.Name)
		return true, &actions{event: e, deleteSession: true}, 0, nil
	}
	sessionUpdate, needsUpdate := NewStatus(session.Status).
		WithExpiresAt(expiresAt).
		AsApplyConfiguration(session)
	if needsUpdate {
		return true, &actions{session: sessionUpdate}, 0, nil
	}
	return false, nil, 0, nil
}

func (c *SessionController) generateAuthorizationPolicy(ctx context.Context, now func() time.Time, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier) (bool, *actions, time.Duration, error) {
	current, err := c.getAuthorizationPolicy(session.Namespace, authorizationPolicyNameForSession(session))
	if err != nil && !apierrors.IsNotFound(err) {
		return true, nil, 0, err
	}

	// original policy creation
	desired := buildAuthorizationPolicy(session)
	if current == nil {
		e := event("AuthorizationPolicyGeneration", "Creating authorization policy for %s/%s.", session.Namespace, session.Name)
		return true, &actions{event: e, authPolicy: desired}, 0, nil
	}

	// policy drift detection
	specDrifted := !proto.Equal(desired.Spec, &current.Spec)
	ownerRefsDrifted := len(current.OwnerReferences) == 0 || current.OwnerReferences[0].UID != session.UID
	if specDrifted || ownerRefsDrifted {
		e := event("AuthorizationPolicyUpdate", "Updating authorization policy for %s/%s.", session.Namespace, session.Name)
		return true, &actions{event: e, authPolicy: desired}, 0, nil
	}

	// record in status
	sessionUpdate, needsUpdate := NewStatus(session.Status).
		WithAuthorizationPolicyRef(current.Name).
		AsApplyConfiguration(session)
	if needsUpdate {
		return true, &actions{session: sessionUpdate}, 0, nil
	}
	return false, nil, 0, nil
}

func authorizationPolicyNameForSession(session *sessiongatev1alpha1.Session) string {
	return session.Name
}

func buildAuthorizationPolicy(session *sessiongatev1alpha1.Session) *securityapplyv1beta1.AuthorizationPolicyApplyConfiguration {
	claim := session.Spec.Owner.UserPrincipal.Claim
	principal := session.Spec.Owner.UserPrincipal.Name
	policyCfg := securityapplyv1beta1.AuthorizationPolicy(session.Name, session.Namespace).
		WithOwnerReferences(metaapplyv1.OwnerReference().
			WithBlockOwnerDeletion(true).
			WithAPIVersion(sessiongatev1alpha1.SchemeGroupVersion.String()).
			WithKind("Session").
			WithName(authorizationPolicyNameForSession(session)).
			WithUID(session.UID).
			WithController(true)).
		WithLabels(map[string]string{
			controller.LabelManagedBy: controller.ControllerAgentName,
		}).
		WithSpec(
			securityv1beta1api.AuthorizationPolicy{
				Selector: &typev1beta1.WorkloadSelector{
					MatchLabels: map[string]string{
						"app.kubernetes.io/name": "sessiongate",
					},
				},
				Action: securityv1beta1api.AuthorizationPolicy_ALLOW,
				Rules: []*securityv1beta1api.Rule{
					{
						To: []*securityv1beta1api.Rule_To{
							{
								Operation: &securityv1beta1api.Operation{
									Paths: []string{
										fmt.Sprintf("/sessiongate/%s/kas/*", session.Name), // todo: use endpoint provider
									},
								},
							},
						},
						When: []*securityv1beta1api.Condition{
							{
								Key:    fmt.Sprintf("request.auth.claims[%s]", claim),
								Values: []string{principal},
							},
						},
					},
				},
			},
		)
	return policyCfg
}

func (c *SessionController) getCredentialSecret(session *sessiongatev1alpha1.Session) (*controller.CredentialSecret, error) {
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

func (c *SessionController) generateCredentials(ctx context.Context, now func() time.Time, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier) (bool, *actions, time.Duration, error) {
	credentialSecret, err := c.getCredentialSecret(session)
	if err != nil {
		return true, nil, 0, err
	}

	// if there is already a certificate in the secret, nothing to do
	if _, certExists := credentialSecret.GetCertificate(); certExists {
		sessionUpdate, needsUpdate := NewStatus(session.Status).
			WithCredentialsSecretRef(session.Name).
			WithConditions(
				applyv1.Condition().
					WithType(string(ConditionTypeCredentialsAvailable)).
					WithStatus(metav1.ConditionTrue).
					WithReason("CredentialsAvailable").
					WithMessage("Credentials available").
					WithObservedGeneration(session.Generation).
					WithLastTransitionTime(metav1.Now()),
			).AsApplyConfiguration(session)
		if needsUpdate {
			return true, &actions{session: sessionUpdate}, 0, nil
		}
		return false, nil, 0, nil
	}

	// the certificate is not yet in the secret, so lets check the CSR and update the secret
	csr, err := mc.GetCSR(ctx, CSRName(session.Name))
	if err != nil && !apierrors.IsNotFound(err) {
		return true, nil, 0, fmt.Errorf("failed to get CSR: %w", err)
	}

	// a CSR exists
	if csr != nil {
		// ... but it's invalid, so we need to delete it and regenerate
		privateKey, privateKeyExists := credentialSecret.GetPrivateKey()
		if !privateKeyExists || !validateCSR(csr, privateKey, session.Spec.Owner.UserPrincipal.Name, session.Spec.AccessLevel.Group) {
			klog.ErrorS(err, "CSR is invalid", "session", session.Name, "namespace", session.Namespace)
			e := event("CSRInvalid", "CSR for %s/%s is invalid, deleting to regenerate.", session.Namespace, session.Name)
			return true, &actions{event: e, deleteCSR: true}, 0, nil
		}
		// ... if it has a certificate, we can bring it to the secret
		if len(csr.Status.Certificate) > 0 {
			return true, &actions{secret: credentialSecret.ApplyConfigurationForCertificate(csr.Status.Certificate)}, 0, nil
		}
		// ... if not, let's handle approval
		if !isCSRApproved(csr) {
			sessionUpdate, needsUpdate := NewStatus(session.Status).
				WithConditions(
					CredentialsNotAvailableCondition("CertificateSigningRequestPending", "Certificate signing request pending, waiting for approval", session.Generation),
					NotReadyCondition(session.Generation),
				).AsApplyConfiguration(session)
			if needsUpdate {
				return true, &actions{session: sessionUpdate}, 0, nil
			}

			csrApproval, err := mc.GetCSRApproval(ctx, session.Spec.HostedControlPlane.Namespace, CSRName(session.Name))
			if err != nil {
				return true, nil, 0, fmt.Errorf("failed to get CSR approval: %w", err)
			}
			if csrApproval == nil {
				return true, &actions{csrApproval: &csrApprovalAction{
					namespace: session.Spec.HostedControlPlane.Namespace,
					approval: certificatesv1alpha1apply.CertificateSigningRequestApproval(
						CSRName(session.Name),
						session.Spec.HostedControlPlane.Namespace,
					),
				}}, 0, nil
			}
			// approval is in place, so we just need to wait some more - the CSR informer will let us know when the CSR changed
			return true, nil, 0, nil
		}
	}

	// there is no CSR yet, so we need to create one
	privateKey, privateKeyExists := credentialSecret.GetPrivateKey()
	if privateKeyExists {
		sessionUpdate, needsUpdate := NewStatus(session.Status).
			WithConditions(
				CredentialsNotAvailableCondition("PrivateKeyCreate", "Private key created, waiting for CSR to be created", session.Generation),
				NotReadyCondition(session.Generation),
			).AsApplyConfiguration(session)
		if needsUpdate {
			return true, &actions{session: sessionUpdate}, 0, nil
		}

		csrApplyConfig, err := createCSRApplyConfiguration(session.Namespace+"/"+session.Name, CSRName(session.Name), session.Spec.HostedControlPlane.Namespace, privateKey, session.Spec.Owner.UserPrincipal.Name, session.Spec.AccessLevel.Group)
		if err != nil {
			return true, nil, 0, fmt.Errorf("failed to create CSR apply configuration: %w", err)
		}
		e := event("CSRGeneration", "Creating CSR for %s/%s on management cluster.", session.Namespace, session.Name)
		return true, &actions{event: e, csr: csrApplyConfig}, 0, nil
	}

	// ... but to create a CSR, we need a private key first
	privateKey, err = c.newPrivateKey(2048)
	if err != nil {
		return true, nil, 0, fmt.Errorf("failed to generate private key: %w", err)
	}
	e := event("PrivateKeyGeneration", "Generating private key for %s/%s.", session.Namespace, session.Name)
	return true, &actions{event: e, secret: credentialSecret.ApplyConfigurationForPrivateKey(privateKey)}, 0, nil
}

func (c *SessionController) finalizeSession(ctx context.Context, now func() time.Time, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier) (bool, *actions, time.Duration, error) {
	needsBackendURL := session.Status.BackendKASURL == ""
	needsEndpoint := session.Status.Endpoint == ""

	if !needsBackendURL && !needsEndpoint {
		return false, nil, 0, nil
	}

	var backendKASURL string

	if needsBackendURL {
		// Get HostedCluster from management cluster
		hcp, err := mc.GetHostedControlPlane(ctx, session.Spec.HostedControlPlane.Namespace)
		if err != nil {
			return true, nil, 5 * time.Second, fmt.Errorf("failed to get HostedCluster: %w", err)
		}
		backendKASURL = fmt.Sprintf("https://%s", hcp.Spec.KubeAPIServerDNSName)
	}

	// build status update
	statusUpdate, needsUpdate := NewStatus(session.Status).
		WithBackendKASURL(backendKASURL).
		WithEndpoint(c.endpointProvider.GetSessionEndpoint(session.Name)).
		WithConditions(
			applyv1.Condition().
				WithType(string(ConditionTypeReady)).
				WithStatus(metav1.ConditionTrue).
				WithReason("Ready").
				WithMessage("Session is ready").
				WithObservedGeneration(session.Generation).
				WithLastTransitionTime(metav1.Now()),
		).
		AsApplyConfiguration(session)
	if needsUpdate {
		e := event("SessionFinalization", "Finalizing session %s/%s with endpoint and backend URL.", session.Namespace, session.Name)
		return true, &actions{event: e, session: statusUpdate}, 0, nil
	}
	return false, nil, 0, nil
}

func createCSRApplyConfiguration(sessionKey, name, namespace string, privateKey *rsa.PrivateKey, user string, organization string) (*certapplyv1.CertificateSigningRequestApplyConfiguration, error) {
	subject := pkix.Name{
		CommonName:   CSRCommonName(user),
		Organization: []string{organization},
	}
	template := x509.CertificateRequest{
		Subject:            subject,
		SignatureAlgorithm: x509.SHA256WithRSA,
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &template, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate request: %w", err)
	}

	// Encode to PEM
	csrPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrDER,
	})
	return certapplyv1.CertificateSigningRequest(name).
		WithLabels(map[string]string{
			controller.LabelManagedBy: controller.ControllerAgentName,
		}).
		WithAnnotations(map[string]string{
			controller.AnnotationSessiongate: sessionKey,
		}).
		WithSpec(certapplyv1.CertificateSigningRequestSpec().
			WithRequest(csrPEM...).
			WithSignerName(fmt.Sprintf("hypershift.openshift.io/%s.sre-break-glass", namespace)).
			WithExpirationSeconds(int32(86353)). // ~24 hours
			WithUsages(
				certificatesv1.UsageClientAuth,
				certificatesv1.UsageDigitalSignature,
			)), nil
}

func CSRCommonName(user string) string {
	return fmt.Sprintf("system:sre-break-glass:%s", user)
}

func CSRName(sessionName string) string {
	return fmt.Sprintf("sessiongate-%s", sessionName)
}

// validateCSR checks if an existing CSR matches the expected private key and session details
func validateCSR(csr *certificatesv1.CertificateSigningRequest, privateKey *rsa.PrivateKey, user, organization string) bool {
	if csr == nil || len(csr.Spec.Request) == 0 {
		klog.ErrorS(nil, "CSR is nil or has no request", "csr", csr.Name)
		return false
	}

	// Parse the PEM-encoded CSR
	block, _ := pem.Decode(csr.Spec.Request)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		klog.ErrorS(nil, "CSR is not a valid PEM-encoded certificate request", "csr", csr.Name)
		return false
	}

	// Parse the certificate request
	parsedCSR, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		klog.ErrorS(err, "Failed to parse certificate request", "csr", csr.Name)
		return false
	}

	// Verify the public key matches our private key
	csrPublicKey, ok := parsedCSR.PublicKey.(*rsa.PublicKey)
	if !ok {
		klog.ErrorS(nil, "CSR public key is not an RSA public key", "csr", csr.Name)
		return false
	}
	if !controller.PrivateKeyAndPublicKeyMatch(privateKey, csrPublicKey) {
		klog.ErrorS(nil, "CSR public key does not match private key", "csr", csr.Name)
		return false
	}

	// Verify the subject fields using common function
	expectedCN := CSRCommonName(user)
	if parsedCSR.Subject.CommonName != expectedCN {
		klog.ErrorS(nil, "CSR common name does not match expected", "csr", csr.Name, "expected", expectedCN, "actual", parsedCSR.Subject.CommonName)
		return false
	}

	if len(parsedCSR.Subject.Organization) != 1 || parsedCSR.Subject.Organization[0] != organization {
		klog.ErrorS(nil, "CSR organization does not match expected", "csr", csr.Name, "expected", organization, "actual", parsedCSR.Subject.Organization)
		return false
	}

	return true
}
