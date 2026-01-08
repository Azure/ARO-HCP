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

	certificatesv1 "k8s.io/api/certificates/v1"

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
	c := &SessionController{
		workqueue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[cache.ObjectName](),
			workqueue.TypedRateLimitingQueueConfig[cache.ObjectName]{
				Name: "SessionControlPlaneController",
			},
		),
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
			klog.InfoS("getting authorization policy", "namespace", namespace, "name", name)
			return istioInformers.Security().V1beta1().AuthorizationPolicies().Lister().AuthorizationPolicies(namespace).Get(name)
		},
		getSecret: func(namespace, name string) (*corev1.Secret, error) {
			return kubeinformers.Core().V1().Secrets().Lister().Secrets(namespace).Get(name)
		},
		getManagementClusterProvider: func(ctx context.Context, resourceID string) (*mc.ManagementClusterProvider, error) {
			return managementClusterProviderBuilder(ctx, resourceID)
		},
		newPrivateKey: func(size int) (*rsa.PrivateKey, error) {
			return rsa.GenerateKey(rand.Reader, size)
		},
	}

	// Register main informer
	if err := c.registerInformer(sessiongateInformers.Sessiongate().V1alpha1().Sessions().Informer(), keyForSession); err != nil {
		return nil, fmt.Errorf("failed to register session informer: %w", err)
	}
	// Register secondary informers
	if err := c.registerInformer(istioInformers.Security().V1beta1().AuthorizationPolicies().Informer(), keyForOwningSession); err != nil {
		return nil, fmt.Errorf("failed to register authorization policy informer: %w", err)
	}
	if err := c.registerInformer(kubeinformers.Core().V1().Secrets().Informer(), keyForOwningSession); err != nil {
		return nil, fmt.Errorf("failed to register secret informer: %w", err)
	}

	return c, nil
}

func (c *SessionController) registerInformer(informer cache.SharedIndexInformer, keyFunc func(obj interface{}) (cache.ObjectName, error)) error {
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := keyFunc(obj)
			if err != nil {
				return
			}
			c.workqueue.Add(key)
		},
		UpdateFunc: func(old, new interface{}) {
			key, err := keyFunc(new)
			if err != nil {
				return
			}
			c.workqueue.Add(key)
		},
		DeleteFunc: func(obj interface{}) {
			key, err := keyFunc(obj)
			if err != nil {
				return
			}
			c.workqueue.Add(key)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to add event handler for informer: %w", err)
	}
	c.cachesToSync = append(c.cachesToSync, informer.HasSynced)
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
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "session", objRef)

	if shutdown {
		return false
	}

	defer c.workqueue.Done(objRef)

	requeueAfter, err := c.syncSession(ctx, objRef.Namespace, objRef.Name)
	if err == nil {
		c.workqueue.Forget(objRef)
		logger.V(6).Info("Successfully synced")

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
			_, err = c.sessiongateClient.SessiongateV1alpha1().Sessions(*action.session.Namespace).ApplyStatus(ctx, action.session, metav1.ApplyOptions{FieldManager: c.fieldManager})
		case action.secret != nil:
			_, err = c.kubeClient.CoreV1().Secrets(*action.secret.Namespace).Apply(ctx, action.secret, metav1.ApplyOptions{FieldManager: c.fieldManager, Force: true})
		case action.authPolicy != nil:
			_, err = c.istioClient.AuthorizationPolicies(*action.authPolicy.Namespace).Apply(ctx, action.authPolicy, metav1.ApplyOptions{FieldManager: c.fieldManager, Force: true})
		case action.csr != nil:
			_, err = mc.KubeClient.CertificatesV1().CertificateSigningRequests().Apply(ctx, action.csr, metav1.ApplyOptions{FieldManager: c.fieldManager, Force: true})
		case action.csrApproval != nil:
			_, err = mc.CertificatesClient.CertificateSigningRequestApprovals(action.csrApproval.namespace).Apply(ctx, action.csrApproval.approval, metav1.ApplyOptions{FieldManager: c.fieldManager, Force: true})
		case action.deleteSession:
			err = c.sessiongateClient.SessiongateV1alpha1().Sessions(*action.session.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
		case action.deleteCSR:
			err = mc.KubeClient.CertificatesV1().CertificateSigningRequests().Delete(ctx, session.Name, metav1.DeleteOptions{})
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
		c.generatePrivateKey,
		// generate a certificate signing request
		c.generateCSR,
		// generate a certificate approval
		c.generateCertificateApproval,
		// extract certificate from CSR and store in secret
		c.extractCertificate,
		// finalize session with endpoint and backend URL
		c.finalizeSession,
	} {
		// each step either handles the current step or hands off to the next one
		done, action, requeue, err := step(ctx, now, session, mc)
		if done {
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

	if session.Status.ExpiresAt == nil {
		cfg := sessiongatv1alpha1applyconfigurations.Session(session.Name, session.Namespace)
		cfg.Status = sessiongatv1alpha1applyconfigurations.SessionStatus().
			WithExpiresAt(expiresAt)
		return true, &actions{session: cfg}, 0, nil
	}
	return false, nil, 0, nil
}

func (c *SessionController) generateAuthorizationPolicy(ctx context.Context, now func() time.Time, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier) (bool, *actions, time.Duration, error) {
	current, err := c.getAuthorizationPolicy(session.Namespace, authorizationPolicyNameForSession(session))
	if err != nil && !apierrors.IsNotFound(err) {
		return false, nil, 0, err
	}

	// original policy creation
	desired := buildAuthorizationPolicy(session)
	if current == nil {
		klog.InfoS("auth policy generation")
		e := event("AuthorizationPolicyGeneration", "Creating authorization policy for %s/%s.", session.Namespace, session.Name)
		return true, &actions{event: e, authPolicy: desired}, 0, nil
	}

	// policy drift detection
	specDrifted := !proto.Equal(desired.Spec, &current.Spec)
	ownerRefsDrifted := len(current.OwnerReferences) == 0 || current.OwnerReferences[0].UID != session.UID
	if specDrifted || ownerRefsDrifted {
		klog.InfoS("auth policy drifted")
		e := event("AuthorizationPolicyUpdate", "Updating authorization policy for %s/%s.", session.Namespace, session.Name)
		return true, &actions{event: e, authPolicy: desired}, 0, nil
	}

	// record in status
	if session.Status.AuthorizationPolicyRef != current.Name {
		klog.InfoS("auth policy ref updated")
		sessionUpdate := sessiongatv1alpha1applyconfigurations.Session(session.Name, session.Namespace)
		sessionUpdate.Status = sessiongatv1alpha1applyconfigurations.SessionStatus().
			WithAuthorizationPolicyRef(current.Name)
		return true, &actions{session: sessionUpdate}, 0, nil
	}
	klog.InfoS("all good here")
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
			WithAPIVersion(sessiongatev1alpha1.SchemeGroupVersion.String()).
			WithKind("Session").
			WithName(authorizationPolicyNameForSession(session)).
			WithUID(session.UID)).
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
										fmt.Sprintf("/sessiongate/%s/kas/*", session.Name),
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

func (c *SessionController) getCredentialSecret(ctx context.Context, session *sessiongatev1alpha1.Session) (*controller.CredentialSecret, error) {
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

func (c *SessionController) generatePrivateKey(ctx context.Context, now func() time.Time, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier) (bool, *actions, time.Duration, error) {
	credentialSecret, err := c.getCredentialSecret(ctx, session)
	if err != nil {
		return false, nil, 0, err
	}

	_, privateKeyExists := credentialSecret.GetPrivateKey()
	if !privateKeyExists {
		// Generate new private key
		privateKey, err := c.newPrivateKey(2048)
		if err != nil {
			return false, nil, 0, fmt.Errorf("failed to generate private key: %w", err)
		}
		e := event("PrivateKeyGeneration", "Generating private key for %s/%s.", session.Namespace, session.Name)
		return true, &actions{event: e, secret: credentialSecret.ApplyConfigurationForPrivateKey(privateKey)}, 0, nil
	}

	// Private key exists, check if status ref needs updating
	if session.Status.CredentialsSecretRef != session.Name {
		sessionUpdate := sessiongatv1alpha1applyconfigurations.Session(session.Name, session.Namespace)
		sessionUpdate.Status = sessiongatv1alpha1applyconfigurations.SessionStatus().
			WithCredentialsSecretRef(session.Name)
		return true, &actions{session: sessionUpdate}, 0, nil
	}

	return false, nil, 0, nil
}

// generateCSR creates a CertificateSigningRequest on the management cluster
func (c *SessionController) generateCSR(ctx context.Context, now func() time.Time, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier) (bool, *actions, time.Duration, error) {
	credentialSecret, err := c.getCredentialSecret(ctx, session)
	if err != nil {
		return false, nil, 0, err
	}
	privateKey, privateKeyExists := credentialSecret.GetPrivateKey()
	if !privateKeyExists {
		return false, nil, 0, fmt.Errorf("private key doesn't exist yet")
	}

	user := session.Spec.Owner.UserPrincipal.Name
	accessGroup := session.Spec.AccessLevel.Group

	// Check if CSR already exists on the management cluster
	existingCSR, err := mc.GetCSR(ctx, session.Name)
	if err != nil && !apierrors.IsNotFound(err) {
		return false, nil, 0, fmt.Errorf("failed to check CSR existence: %w", err)
	}

	// If CSR exists, validate it
	if existingCSR != nil {
		if validateCSR(existingCSR, privateKey, user, accessGroup) {
			// CSR is valid, skip this step
			return false, nil, 0, nil
		}
		// CSR exists but is invalid (wrong key or subject) - delete it
		e := event("CSRInvalid", "CSR for %s/%s is invalid (mismatched key or subject), deleting and recreating.", session.Namespace, session.Name)
		return true, &actions{event: e, deleteCSR: true}, 0, nil
	}

	// The HCP namespace on the management cluster
	// This is used in the signer name
	hcpNamespace := session.Spec.HostedControlPlane.Namespace

	csrApplyConfig, err := createCSRApplyConfiguration(session.Name, hcpNamespace, privateKey, user, accessGroup)
	if err != nil {
		return false, nil, 0, fmt.Errorf("failed to create CSR apply configuration: %w", err)
	}

	e := event("CSRGeneration", "Creating CSR for %s/%s on management cluster.", session.Namespace, session.Name)
	return true, &actions{event: e, csr: csrApplyConfig}, 0, nil
}

// generateCertificateApproval creates a Hypershift CertificateSigningRequestApproval on the management cluster
// This step requires that the CSR has already been created by generateCSR
func (c *SessionController) generateCertificateApproval(ctx context.Context, now func() time.Time, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier) (bool, *actions, time.Duration, error) {
	// Build the CSR approval namespace (HCP namespace on management cluster)
	csrApprovalNamespace := session.Spec.HostedControlPlane.Namespace

	// Check if CSR approval already exists on management cluster
	existingApproval, err := mc.GetCSRApproval(ctx, csrApprovalNamespace, session.Name)
	if err != nil && !apierrors.IsNotFound(err) {
		return false, nil, 0, fmt.Errorf("failed to check CSR approval existence: %w", err)
	}

	// If approval already exists and has the correct labels, skip this step
	if existingApproval != nil {
		if existingApproval.Labels != nil {
			if existingApproval.Labels["api.openshift.com/type"] == "break-glass-credential" {
				// Approval exists with correct labels, nothing to do
				return false, nil, 0, nil
			}
		}
		// Approval exists but needs update (fall through to create action)
	}

	// Build the desired CSR approval
	approvalApplyConfig := certificatesv1alpha1apply.CertificateSigningRequestApproval(session.Name, csrApprovalNamespace).
		WithLabels(map[string]string{
			"api.openshift.com/type": "break-glass-credential",
		})

	var eventReason string
	if existingApproval != nil {
		eventReason = "CertificateApprovalUpdate"
	} else {
		eventReason = "CertificateApprovalGeneration"
	}

	e := event(eventReason, "Creating/updating CSR approval for %s/%s in management cluster namespace %s.", session.Namespace, session.Name, csrApprovalNamespace)
	return true, &actions{event: e, csrApproval: &csrApprovalAction{
		namespace: csrApprovalNamespace,
		approval:  approvalApplyConfig,
	}}, 0, nil
}

func (c *SessionController) extractCertificate(ctx context.Context, now func() time.Time, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier) (bool, *actions, time.Duration, error) {
	// Get current credential secret first to check if certificate already exists
	credentialSecret, err := c.getCredentialSecret(ctx, session)
	if err != nil {
		return false, nil, 0, err
	}

	// Check if certificate is already stored
	if certificate, exists := credentialSecret.GetCertificate(); exists && len(certificate) > 0 {
		// Certificate already stored, nothing to do
		return false, nil, 0, nil
	}

	// Get the CSR from the management cluster
	csr, err := mc.GetCSR(ctx, session.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// CSR doesn't exist yet, should not happen at this stage
			return false, nil, 0, fmt.Errorf("CSR not found")
		}
		return false, nil, 0, fmt.Errorf("failed to get CSR: %w", err)
	}

	// Check if certificate has been issued
	if len(csr.Status.Certificate) == 0 {
		// Certificate not issued yet, requeue to wait for signer
		return true, nil, 5 * time.Second, nil // once we have informers for the MC, we don't need to requeue
	}

	// Store the certificate in the secret
	e := event("CertificateExtraction", "Extracting certificate from CSR for %s/%s.", session.Namespace, session.Name)
	return true, &actions{
		event:  e,
		secret: credentialSecret.ApplyConfigurationForCertificate(csr.Status.Certificate),
	}, 0, nil
}

func (c *SessionController) finalizeSession(ctx context.Context, now func() time.Time, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier) (bool, *actions, time.Duration, error) {
	needsBackendURL := session.Status.BackendKASURL == ""
	needsEndpoint := session.Status.Endpoint == ""

	if !needsBackendURL && !needsEndpoint {
		// Already finalized
		return false, nil, 0, nil
	}

	var backendKASURL, endpoint string

	if needsBackendURL {
		// Get HostedCluster from management cluster
		hcp, err := mc.GetHostedCluster(ctx, session.Spec.HostedControlPlane.Namespace)
		if err != nil {
			return false, nil, 5 * time.Second, fmt.Errorf("failed to get HostedCluster: %w", err)
		}
		backendKASURL = fmt.Sprintf("https://%s", hcp.Spec.KubeAPIServerDNSName)
	}

	if needsEndpoint {
		endpoint = c.endpointProvider.GetSessionEndpoint(session.Name)
	}

	// Build status update
	sessionUpdate := sessiongatv1alpha1applyconfigurations.Session(session.Name, session.Namespace)
	statusUpdate := sessiongatv1alpha1applyconfigurations.SessionStatus()

	if needsBackendURL {
		statusUpdate = statusUpdate.WithBackendKASURL(backendKASURL)
	}
	if needsEndpoint {
		statusUpdate = statusUpdate.WithEndpoint(endpoint)
	}

	sessionUpdate.Status = statusUpdate
	e := event("SessionFinalization", "Finalizing session %s/%s with endpoint and backend URL.", session.Namespace, session.Name)
	return true, &actions{event: e, session: sessionUpdate}, 0, nil
}

func createCSRApplyConfiguration(name, namespace string, privateKey *rsa.PrivateKey, user string, organization string) (*certapplyv1.CertificateSigningRequestApplyConfiguration, error) {
	subject := pkix.Name{
		CommonName:   buildCommonName(user),
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
		WithSpec(certapplyv1.CertificateSigningRequestSpec().
			WithRequest(csrPEM...).
			WithSignerName(fmt.Sprintf("hypershift.openshift.io/%s.sre-break-glass", namespace)).
			WithExpirationSeconds(int32(86353)). // ~24 hours
			WithUsages(
				certificatesv1.UsageClientAuth,
				certificatesv1.UsageDigitalSignature,
			)), nil
}

// buildCommonName generates the CommonName for break-glass CSR certificates
func buildCommonName(user string) string {
	return fmt.Sprintf("system:sre-break-glass:%s", user)
}

// validateCSR checks if an existing CSR matches the expected private key and session details
func validateCSR(csr *certificatesv1.CertificateSigningRequest, privateKey *rsa.PrivateKey, user, organization string) bool {
	if csr == nil || len(csr.Spec.Request) == 0 {
		return false
	}

	// Parse the PEM-encoded CSR
	block, _ := pem.Decode(csr.Spec.Request)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return false
	}

	// Parse the certificate request
	parsedCSR, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return false
	}

	// Verify the public key matches our private key
	expectedPublicKey := &privateKey.PublicKey
	csrPublicKey, ok := parsedCSR.PublicKey.(*rsa.PublicKey)
	if !ok {
		return false
	}
	if expectedPublicKey.N.Cmp(csrPublicKey.N) != 0 || expectedPublicKey.E != csrPublicKey.E {
		return false
	}

	// Verify the subject fields using common function
	expectedCN := buildCommonName(user)
	if parsedCSR.Subject.CommonName != expectedCN {
		return false
	}

	if len(parsedCSR.Subject.Organization) != 1 || parsedCSR.Subject.Organization[0] != organization {
		return false
	}

	return true
}
