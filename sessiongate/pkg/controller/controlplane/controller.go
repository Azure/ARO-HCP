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

	"github.com/openshift/library-go/pkg/controller/factory"
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
	"k8s.io/apimachinery/pkg/runtime"
	corev1applyconfigurations "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"

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
) factory.Controller {
	c := &SessionController{
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
			return managementClusterProviderBuilder(ctx, resourceID)
		},
		newPrivateKey: func(size int) (*rsa.PrivateKey, error) {
			return rsa.GenerateKey(rand.Reader, size)
		},
	}

	return factory.New().
		WithInformersQueueKeysFunc(enqueueSession, sessiongateInformers.Sessiongate().V1alpha1().Sessions().Informer()).
		WithInformersQueueKeysFunc(
			controller.EnqueueOwningSession,
			istioInformers.Security().V1beta1().AuthorizationPolicies().Informer(),
			kubeinformers.Core().V1().Secrets().Informer(),
		).
		WithSync(c.syncSession).
		ResyncEvery(time.Minute*5).
		ToController("SessionController", eventRecorder.WithComponentSuffix(c.fieldManager))
}

func enqueueSession(obj runtime.Object) []string {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		klog.ErrorS(err, "could not determine queue key")
		return nil
	}
	return []string{key}
}

func (c *SessionController) syncSession(ctx context.Context, syncContext factory.SyncContext) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(syncContext.QueueKey())
	if err != nil {
		return err
	}

	session, err := c.getSession(namespace, name)
	if err != nil && apierrors.IsNotFound(err) {
		return nil // nothing to be done, Session is gone
	} else if err != nil {
		return err
	}

	mc, err := c.getManagementClusterProvider(ctx, session.Spec.ManagementCluster.ResourceID)
	if err != nil {
		return err
	}

	action, requeue, err := c.processSession(ctx, session, mc, nil)
	if err != nil {
		return err
	}
	if requeue {
		return factory.SyntheticRequeueError
	}
	if action != nil {
		if err := action.validate(); err != nil {
			panic(err) // if validation fails, we have a programming error
		}
		if action.event != nil {
			syncContext.Recorder().Eventf(action.event.reason, action.event.messageFmt, action.event.args...)
		}

		switch {
		case action.session != nil:
			_, err := c.sessiongateClient.SessiongateV1alpha1().Sessions(*action.session.Namespace).ApplyStatus(ctx, action.session, metav1.ApplyOptions{FieldManager: c.fieldManager})
			return err
		case action.secret != nil:
			_, err := c.kubeClient.CoreV1().Secrets(*action.secret.Namespace).Apply(ctx, action.secret, metav1.ApplyOptions{FieldManager: c.fieldManager, Force: true})
			return err
		case action.authPolicy != nil:
			_, err := c.istioClient.AuthorizationPolicies(*action.authPolicy.Namespace).Apply(ctx, action.authPolicy, metav1.ApplyOptions{FieldManager: c.fieldManager, Force: true})
			return err
		case action.csr != nil:
			_, err := mc.KubeClient.CertificatesV1().CertificateSigningRequests().Apply(ctx, action.csr, metav1.ApplyOptions{FieldManager: c.fieldManager, Force: true})
			return err
		case action.csrApproval != nil:
			_, err := mc.CertificatesClient.CertificateSigningRequestApprovals(action.csrApproval.namespace).Apply(ctx, action.csrApproval.approval, metav1.ApplyOptions{FieldManager: c.fieldManager, Force: true})
			return err
		case action.deleteSession:
			return c.sessiongateClient.SessiongateV1alpha1().Sessions(*action.session.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
		case action.deleteCSR:
			mc.KubeClient.CertificatesV1().CertificateSigningRequests().Delete(ctx, session.Name, metav1.DeleteOptions{})
		}
	}

	return nil
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

func (c *SessionController) processSession(ctx context.Context, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier, now func() time.Time) (*actions, bool, error) {
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
	return nil, false, nil
}

// sessionStep is a step in the session reconciliation process
// returns:
// - done: whether the current reconciliation loop should stop with the current step result
// - action: the action to take
// - requeue: whether to requeue the session
// - error: an error that occurred
type sessionStep func(ctx context.Context, now func() time.Time, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier) (bool, *actions, bool, error)

func (c *SessionController) handleExpiration(ctx context.Context, now func() time.Time, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier) (bool, *actions, bool, error) {
	expiresAt := metav1.NewTime(session.CreationTimestamp.Add(session.Spec.TTL.Duration))
	if now().After(expiresAt.Time) {
		e := event("SessionExpiration", "Session has expired, deleting %s/%s.", session.Namespace, session.Name)
		return true, &actions{event: e, deleteSession: true}, false, nil
	}

	if session.Status.ExpiresAt == nil {
		cfg := sessiongatv1alpha1applyconfigurations.Session(session.Name, session.Namespace)
		cfg.Status = sessiongatv1alpha1applyconfigurations.SessionStatus().
			WithExpiresAt(expiresAt)
		return true, &actions{session: cfg}, false, nil
	}
	return false, nil, false, nil
}

func (c *SessionController) generateAuthorizationPolicy(ctx context.Context, now func() time.Time, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier) (bool, *actions, bool, error) {
	current, err := c.getAuthorizationPolicy(session.Namespace, session.Name)
	if err != nil && !apierrors.IsNotFound(err) {
		return false, nil, false, err
	}

	// original policy creation
	desired := buildAuthorizationPolicy(session)
	if current == nil {
		e := event("AuthorizationPolicyGeneration", "Creating authorization policy for %s/%s.", session.Namespace, session.Name)
		return true, &actions{event: e, authPolicy: desired}, false, nil
	}

	// policy drift detection
	specDrifted := !proto.Equal(desired.Spec, &current.Spec)
	ownerRefsDrifted := len(current.OwnerReferences) == 0 || current.OwnerReferences[0].UID != session.UID
	if specDrifted || ownerRefsDrifted {
		e := event("AuthorizationPolicyUpdate", "Updating authorization policy for %s/%s.", session.Namespace, session.Name)
		return true, &actions{event: e, authPolicy: desired}, false, nil
	}

	// record in status
	if session.Status.AuthorizationPolicyRef != current.Name {
		sessionUpdate := sessiongatv1alpha1applyconfigurations.Session(session.Name, session.Namespace)
		sessionUpdate.Status = sessiongatv1alpha1applyconfigurations.SessionStatus().
			WithAuthorizationPolicyRef(current.Name)
		return true, &actions{session: sessionUpdate}, false, nil
	}
	return false, nil, false, nil
}

func buildAuthorizationPolicy(session *sessiongatev1alpha1.Session) *securityapplyv1beta1.AuthorizationPolicyApplyConfiguration {
	claim := session.Spec.Owner.UserPrincipal.Claim
	principal := session.Spec.Owner.UserPrincipal.Name
	policyCfg := securityapplyv1beta1.AuthorizationPolicy(session.Name, session.Namespace).
		WithOwnerReferences(metaapplyv1.OwnerReference().
			WithAPIVersion(sessiongatev1alpha1.SchemeGroupVersion.String()).
			WithKind("Session").
			WithName(session.Name).
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

func (c *SessionController) generatePrivateKey(ctx context.Context, now func() time.Time, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier) (bool, *actions, bool, error) {
	credentialSecret, err := c.getCredentialSecret(ctx, session)
	if err != nil {
		return false, nil, false, err
	}

	_, privateKeyExists := credentialSecret.GetPrivateKey()
	if !privateKeyExists {
		// Generate new private key
		privateKey, err := c.newPrivateKey(2048)
		if err != nil {
			return false, nil, false, fmt.Errorf("failed to generate private key: %w", err)
		}
		e := event("PrivateKeyGeneration", "Generating private key for %s/%s.", session.Namespace, session.Name)
		return true, &actions{event: e, secret: credentialSecret.ApplyConfigurationForPrivateKey(privateKey)}, false, nil
	}

	// Private key exists, check if status ref needs updating
	if session.Status.CredentialsSecretRef != session.Name {
		sessionUpdate := sessiongatv1alpha1applyconfigurations.Session(session.Name, session.Namespace)
		sessionUpdate.Status = sessiongatv1alpha1applyconfigurations.SessionStatus().
			WithCredentialsSecretRef(session.Name)
		return true, &actions{session: sessionUpdate}, false, nil
	}

	return false, nil, false, nil
}

// generateCSR creates a CertificateSigningRequest on the management cluster
func (c *SessionController) generateCSR(ctx context.Context, now func() time.Time, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier) (bool, *actions, bool, error) {
	credentialSecret, err := c.getCredentialSecret(ctx, session)
	if err != nil {
		return false, nil, false, err
	}
	privateKey, privateKeyExists := credentialSecret.GetPrivateKey()
	if !privateKeyExists {
		return false, nil, false, fmt.Errorf("private key doesn't exist yet")
	}

	user := session.Spec.Owner.UserPrincipal.Name
	accessGroup := session.Spec.AccessLevel.Group

	// Check if CSR already exists on the management cluster
	existingCSR, err := mc.GetCSR(ctx, session.Name)
	if err != nil && !apierrors.IsNotFound(err) {
		return false, nil, false, fmt.Errorf("failed to check CSR existence: %w", err)
	}

	// If CSR exists, validate it
	if existingCSR != nil {
		if validateCSR(existingCSR, privateKey, user, accessGroup) {
			// CSR is valid, skip this step
			return false, nil, false, nil
		}
		// CSR exists but is invalid (wrong key or subject) - delete it
		e := event("CSRInvalid", "CSR for %s/%s is invalid (mismatched key or subject), deleting and recreating.", session.Namespace, session.Name)
		return true, &actions{event: e, deleteCSR: true}, false, nil
	}

	// The HCP namespace on the management cluster
	// This is used in the signer name
	hcpNamespace := session.Spec.HostedControlPlane.Namespace

	csrApplyConfig, err := createCSRApplyConfiguration(session.Name, hcpNamespace, privateKey, user, accessGroup)
	if err != nil {
		return false, nil, false, fmt.Errorf("failed to create CSR apply configuration: %w", err)
	}

	e := event("CSRGeneration", "Creating CSR for %s/%s on management cluster.", session.Namespace, session.Name)
	return true, &actions{event: e, csr: csrApplyConfig}, false, nil
}

// generateCertificateApproval creates a Hypershift CertificateSigningRequestApproval on the management cluster
// This step requires that the CSR has already been created by generateCSR
func (c *SessionController) generateCertificateApproval(ctx context.Context, now func() time.Time, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier) (bool, *actions, bool, error) {
	// Build the CSR approval namespace (HCP namespace on management cluster)
	csrApprovalNamespace := session.Spec.HostedControlPlane.Namespace

	// Check if CSR approval already exists on management cluster
	existingApproval, err := mc.GetCSRApproval(ctx, csrApprovalNamespace, session.Name)
	if err != nil && !apierrors.IsNotFound(err) {
		return false, nil, false, fmt.Errorf("failed to check CSR approval existence: %w", err)
	}

	// If approval already exists and has the correct labels, skip this step
	if existingApproval != nil {
		if existingApproval.Labels != nil {
			if existingApproval.Labels["api.openshift.com/type"] == "break-glass-credential" {
				// Approval exists with correct labels, nothing to do
				return false, nil, false, nil
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
	}}, false, nil
}

func (c *SessionController) extractCertificate(ctx context.Context, now func() time.Time, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier) (bool, *actions, bool, error) {
	// Get current credential secret first to check if certificate already exists
	credentialSecret, err := c.getCredentialSecret(ctx, session)
	if err != nil {
		return false, nil, false, err
	}

	// Check if certificate is already stored
	if certificate, exists := credentialSecret.GetCertificate(); exists && len(certificate) > 0 {
		// Certificate already stored, nothing to do
		return false, nil, false, nil
	}

	// Get the CSR from the management cluster
	csr, err := mc.GetCSR(ctx, session.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// CSR doesn't exist yet, should not happen at this stage
			return false, nil, false, fmt.Errorf("CSR not found")
		}
		return false, nil, false, fmt.Errorf("failed to get CSR: %w", err)
	}

	// Check if certificate has been issued
	if len(csr.Status.Certificate) == 0 {
		// Certificate not issued yet, requeue to wait for signer
		return true, nil, true, nil
	}

	// Store the certificate in the secret
	e := event("CertificateExtraction", "Extracting certificate from CSR for %s/%s.", session.Namespace, session.Name)
	return true, &actions{
		event:  e,
		secret: credentialSecret.ApplyConfigurationForCertificate(csr.Status.Certificate),
	}, false, nil
}

func (c *SessionController) finalizeSession(ctx context.Context, now func() time.Time, session *sessiongatev1alpha1.Session, mc mc.ManagementClusterQuerier) (bool, *actions, bool, error) {
	needsBackendURL := session.Status.BackendKASURL == ""
	needsEndpoint := session.Status.Endpoint == ""

	if !needsBackendURL && !needsEndpoint {
		// Already finalized
		return false, nil, false, nil
	}

	var backendKASURL, endpoint string

	if needsBackendURL {
		// Get HostedCluster from management cluster
		hcp, err := mc.GetHostedCluster(ctx, session.Spec.HostedControlPlane.Namespace)
		if err != nil {
			if apierrors.IsNotFound(err) {
				// HCP not found yet, requeue to wait
				return true, nil, true, nil
			}
			return false, nil, false, fmt.Errorf("failed to get HostedCluster: %w", err)
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
	return true, &actions{event: e, session: sessionUpdate}, false, nil
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
