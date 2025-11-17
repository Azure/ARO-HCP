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
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/time/rate"
	securityv1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	istioclientset "istio.io/client-go/pkg/clientset/versioned/typed/security/v1beta1"
	istioinformers "istio.io/client-go/pkg/informers/externalversions/security/v1beta1"
	istiolisters "istio.io/client-go/pkg/listers/security/v1beta1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/controller"
	sessiongateapply "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/applyconfiguration/sessiongate/v1alpha1"
	clientset "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/clientset/versioned"
	sessiongateschema "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/clientset/versioned/scheme"
	informers "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/informers/externalversions/sessiongate/v1alpha1"
	listers "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/listers/sessiongate/v1alpha1"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/mc"
)

// control plane controller implementation for Session resources.
// it runs with leader election and handles Session reconciliation into
// - istio AuthorizationPolicies
// - session credentials secrets
type Controller struct {
	kubeclientset           kubernetes.Interface
	sessiongateclientset    clientset.Interface
	istioclientset          istioclientset.SecurityV1beta1Interface
	sessionsLister          listers.SessionLister
	sessionsSynced          cache.InformerSynced
	authzPoliciesLister     istiolisters.AuthorizationPolicyLister
	authzPoliciesSynced     cache.InformerSynced
	workqueue               workqueue.TypedRateLimitingInterface[cache.ObjectName]
	registry                controller.SessionRegistry
	hcpProviderBuilder      mc.HCPProviderBuilder
	credentialProvider      controller.CredentialProvider
	sessionNamespace        string
	secretsLister           corev1listers.SecretLister
	secretsSynced           cache.InformerSynced
	leaderElectionConfig    *controller.LeaderElectionConfig
	credentialCheckInterval time.Duration
	logger                  klog.Logger
}

func NewController(
	ctx context.Context,
	logger klog.Logger,
	kubeclientset kubernetes.Interface,
	sessiongateclientset clientset.Interface,
	istioclientset istioclientset.SecurityV1beta1Interface,
	sessionsInformer informers.SessionInformer,
	authzPolicyInformer istioinformers.AuthorizationPolicyInformer,
	secretsInformer cache.SharedIndexInformer,
	registry controller.SessionRegistry,
	hcpProviderBuilder mc.HCPProviderBuilder,
	credentialProvider controller.CredentialProvider,
	sessionNamespace string,
	leaderElectionConfig *controller.LeaderElectionConfig,
	credentialCheckInterval time.Duration) (*Controller, error) {

	utilruntime.Must(sessiongateschema.AddToScheme(scheme.Scheme))

	ratelimiter := workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[cache.ObjectName](5*time.Millisecond, 1000*time.Second),
		&workqueue.TypedBucketRateLimiter[cache.ObjectName]{Limiter: rate.NewLimiter(rate.Limit(50), 300)},
	)

	c := &Controller{
		kubeclientset:           kubeclientset,
		sessiongateclientset:    sessiongateclientset,
		istioclientset:          istioclientset,
		sessionsLister:          sessionsInformer.Lister(),
		sessionsSynced:          sessionsInformer.Informer().HasSynced,
		authzPoliciesLister:     authzPolicyInformer.Lister(),
		authzPoliciesSynced:     authzPolicyInformer.Informer().HasSynced,
		secretsLister:           corev1listers.NewSecretLister(secretsInformer.GetIndexer()),
		secretsSynced:           secretsInformer.HasSynced,
		workqueue:               workqueue.NewTypedRateLimitingQueue(ratelimiter),
		registry:                registry,
		hcpProviderBuilder:      hcpProviderBuilder,
		credentialProvider:      credentialProvider,
		sessionNamespace:        sessionNamespace,
		leaderElectionConfig:    leaderElectionConfig,
		credentialCheckInterval: credentialCheckInterval,
		logger:                  logger,
	}

	logger.V(2).Info("Setting up event handlers for control plane controller")

	// Session Informer for control plane
	// enqueues Sessions for reconciliation
	if _, err := sessionsInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: c.enqueueSession,
		UpdateFunc: func(old, new interface{}) {
			c.enqueueSession(new)
		},
		DeleteFunc: c.enqueueSession,
	}); err != nil {
		return nil, fmt.Errorf("failed to add event handler for sessions (control plane): %w", err)
	}

	// Secret Informer for control plane
	// drift detection - deletions or changes of secrets outside of Session lifecycle
	if _, err := secretsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: c.enqueueOwningSession,
		UpdateFunc: func(old, new interface{}) {

			newSecret := new.(*corev1.Secret)
			oldSecret := old.(*corev1.Secret)
			if newSecret.ResourceVersion == oldSecret.ResourceVersion {
				return
			}
			c.enqueueOwningSession(new)
		},
		DeleteFunc: c.enqueueOwningSession,
	}); err != nil {
		return nil, fmt.Errorf("failed to add event handler for secrets: %w", err)
	}

	// AuthorizationPolicy Informer for control plane
	// drift detection - deletions or changes of policies outside of Session lifecycle
	if _, err := authzPolicyInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: c.enqueueOwningSession,
		UpdateFunc: func(old, new interface{}) {
			newPolicy := new.(*securityv1beta1.AuthorizationPolicy)
			oldPolicy := old.(*securityv1beta1.AuthorizationPolicy)
			if newPolicy.ResourceVersion == oldPolicy.ResourceVersion {
				return
			}
			c.enqueueOwningSession(new)
		},
		DeleteFunc: c.enqueueOwningSession,
	}); err != nil {
		return nil, fmt.Errorf("failed to add event handler for authorization policies: %w", err)
	}

	return c, nil
}

// Run participates in leader election and runs controller workers when elected leader
func (c *Controller) Run(ctx context.Context, workers int) error {
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed to get hostname for leader election: %w", err)
	}

	// Create leader election lock
	lock, err := resourcelock.NewFromKubeconfig(
		resourcelock.LeasesResourceLock,
		c.leaderElectionConfig.Namespace,
		c.leaderElectionConfig.LockName,
		resourcelock.ResourceLockConfig{
			Identity: hostname,
		},
		c.leaderElectionConfig.KubeConfig,
		c.leaderElectionConfig.RenewDeadline,
	)
	if err != nil {
		return fmt.Errorf("failed to create leader election lock: %w", err)
	}

	c.logger.V(2).Info("Leader election configured",
		"lockName", c.leaderElectionConfig.LockName,
		"identity", hostname,
		"leaseDuration", c.leaderElectionConfig.LeaseDuration,
		"renewDeadline", c.leaderElectionConfig.RenewDeadline,
		"retryPeriod", c.leaderElectionConfig.RetryPeriod)

	// Create leader elector
	le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   c.leaderElectionConfig.LeaseDuration,
		RenewDeadline:   c.leaderElectionConfig.RenewDeadline,
		RetryPeriod:     c.leaderElectionConfig.RetryPeriod,
		ReleaseOnCancel: true,
		Name:            c.leaderElectionConfig.LockName,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(leaderCtx context.Context) {
				c.logger.Info("Acquired leadership - starting control plane controller workers")

				if err := c.run(leaderCtx, workers); err != nil {
					c.logger.Error(err, "Control plane controller stopped with error")
				}
			},
			OnStoppedLeading: func() {
				c.logger.Info("Lost leadership - control plane controller workers stopped")
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create leader elector: %w", err)
	}

	c.logger.Info("Starting leader election for control plane controller")
	le.Run(ctx)
	return nil
}

// run starts the controller workers and blocks until the context is cancelled
func (c *Controller) run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	c.logger.V(2).Info("Starting control plane controller... waiting for informer caches to sync")

	if ok := cache.WaitForCacheSync(ctx.Done(), c.sessionsSynced, c.secretsSynced, c.authzPoliciesSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	sessions, err := c.sessionsLister.Sessions(c.sessionNamespace).List(labels.Everything())
	if err != nil {
		return fmt.Errorf("failed to list sessions for initial reconciliation: %w", err)
	}
	for _, session := range sessions {
		c.enqueueSession(session)
	}
	c.logger.V(2).Info("Enqueued Sessions for reconciliation", "count", len(sessions))

	c.logger.V(2).Info("Starting workers", "count", workers)
	for range workers {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	c.logger.V(2).Info("Started workers")
	<-ctx.Done()
	c.logger.V(2).Info("Shutting down workers")

	return nil
}

// runWorker continually calls processNextWorkItem to read and process messages on the workqueue
func (c *Controller) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem reads a single work item off the workqueue and attempts to process it
func (c *Controller) processNextWorkItem(ctx context.Context) bool {
	objRef, shutdown := c.workqueue.Get()
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "session", objRef)

	if shutdown {
		return false
	}

	defer c.workqueue.Done(objRef)

	requeueAfter, err := c.workCeremony(ctx, logger, objRef)
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

func (c *Controller) workCeremony(ctx context.Context, logger klog.Logger, objRef cache.ObjectName) (time.Duration, error) {
	session, err := c.sessionsLister.Sessions(objRef.Namespace).Get(objRef.Name)
	if err != nil {
		// Session no longer exists - nothing to reconcile
		if errors.IsNotFound(err) {
			return 0, nil
		}

		return 0, err
	}

	sessionCopy := session.DeepCopy()
	sessionCopy.InitializeConditions()

	requeueAfter, err := c.syncHandler(ctx, logger, sessionCopy)

	if patchErr := c.patchSessionStatus(ctx, logger, sessionCopy); patchErr != nil {
		logger.Error(patchErr, "Failed to patch session status")
		// return the patch error only if there was no sync error
		if err == nil {
			return requeueAfter, patchErr
		}
	}

	return requeueAfter, err
}

// syncHandler reconciles a session to desired state and returns requeue duration (0 means no requeue)
func (c *Controller) syncHandler(ctx context.Context, logger klog.Logger, session *sessiongatev1alpha1.Session) (time.Duration, error) {
	//
	// Phase 0: skip reconciliation if Session is being deleted
	//

	if !session.DeletionTimestamp.IsZero() {
		// Session is being deleted - nothing to reconcile
		// cleanup of dependent resources happens via owner references
		return 0, nil
	}

	//
	// Phase 1: initialization, expiration, validation
	//

	// calculate expiration time if not set
	var expiresAt *metav1.Time
	if session.Status.ExpiresAt == nil {
		now := metav1.Now()
		expirationTime := metav1.NewTime(now.Add(session.Spec.TTL.Duration))
		expiresAt = &expirationTime
	} else {
		expiresAt = session.Status.ExpiresAt
	}
	session.Status.ExpiresAt = expiresAt

	// check for expiration
	timeUntilExpiration := time.Until(expiresAt.Time)
	if timeUntilExpiration <= 0 {
		logger.Info("Session has expired, deleting", "session", session.Name, "expiresAt", expiresAt.Time)
		session.MarkSessionInactive(sessiongatev1alpha1.ReasonExpired, "Session has expired")
		session.StopProgressing(sessiongatev1alpha1.ReasonExpired, "Session has expired")
		if err := c.sessiongateclientset.SessiongateV1alpha1().Sessions(session.Namespace).Delete(ctx, session.Name, metav1.DeleteOptions{}); err != nil {
			return 0, err
		}
		return 0, nil
	}

	// validation
	if err := validateSession(session); err != nil {
		session.StopProgressing(sessiongatev1alpha1.ReasonInvalidConfiguration, err.Error())
		logger.Error(err, "Session has invalid configuration")
		return 0, nil
	}

	//
	// Phase 2: authorization policy
	//

	changed, err := c.ensureAuthorizationPolicy(ctx, session)
	if err != nil {
		logger.Error(err, "Failed to ensure AuthorizationPolicy")
		session.MarkAuthorizationPolicyNotReady(sessiongatev1alpha1.ReasonAuthorizationFailed, fmt.Sprintf("Failed to ensure authorization policy: %v", err))
		return 0, err
	}
	if changed {
		session.Progressing(sessiongatev1alpha1.ReasonConfiguringAuthorization, "Authorization policy configured")
		logger.V(2).Info("AuthorizationPolicy changed, waiting for informer notification")
		return 0, nil
	}
	session.MarkAuthorizationPolicyReady()

	//
	// Phase 3: credential provisioning
	//

	credReq := controller.CredentialRequest{
		SessionName:         session.Name,
		SessionUID:          session.UID,
		ManagementClusterID: session.Spec.ManagementCluster.ResourceID,
		HCPNamespace:        session.Spec.HostedControlPlane.Namespace,
		UserPrincipalName:   session.Spec.Owner.UserPrincipal.Name,
		AccessLevelGroup:    session.Spec.AccessLevel.Group,
	}

	result, err := c.credentialProvider.EnsureCredentials(ctx, credReq)
	if err != nil {
		session.MarkCredentialsNotReady(sessiongatev1alpha1.ReasonCredentialMintingFailed, fmt.Sprintf("Failed to provision credentials: %v", err))
		logger.Error(err, "Failed to provision credentials")
		return 0, err
	}

	// Update status fields from result
	session.Status.CredentialsSecretRef = result.SecretName

	switch result.Status {
	case controller.CredentialStatusHostedControlPlaneNotFound:
		logger.V(2).Info("HostedControlPlane not found, will retry when it becomes available")
		session.MarkCredentialsNotReady(sessiongatev1alpha1.ReasonHostedControlPlaneNotFound, "HostedControlPlane not yet available")
		session.Progressing(sessiongatev1alpha1.ReasonHostedControlPlaneNotFound, "Waiting for HostedControlPlane to be created")
		return 0, nil

	case controller.CredentialStatusPrivateKeyCreated:
		logger.V(2).Info("Private key created", "secretName", result.SecretName)
		session.MarkCredentialsNotReady(sessiongatev1alpha1.ReasonPrivateKeyCreated, "Private key generated, preparing certificate request")
		session.Progressing(sessiongatev1alpha1.ReasonMintingCredentials, "Creating certificate signing request")
		return 0, nil

	case controller.CredentialStatusCertificatePending:
		logger.V(2).Info("Certificate pending, will poll on next reconcile", "secretName", result.SecretName)
		session.MarkCredentialsNotReady(sessiongatev1alpha1.ReasonCertificatePending, "Certificate signing request submitted. Waiting for certificate")
		session.Progressing(sessiongatev1alpha1.ReasonMintingCredentials, "Waiting for certificate")
		return c.credentialCheckInterval, nil

	case controller.CredentialStatusReady:
		logger.V(2).Info("Credentials ready", "secretName", result.SecretName)
		session.MarkCredentialsReady()

	default:
		err := fmt.Errorf("unknown credential status: %d", result.Status)
		logger.Error(err, "Unknown credential status", "status", result.Status)
		session.MarkCredentialsNotReady(sessiongatev1alpha1.ReasonCredentialsFailed, err.Error())
		return 0, err
	}

	//
	// Phase 4: Network path
	//

	// this part will be revamped soon to embrace port-forwarding to reach private HCPs

	hcpprovider, err := c.hcpProviderBuilder(ctx, session.Spec.ManagementCluster.ResourceID)
	if err != nil {
		logger.Error(err, "Failed to get HCP provider", "mgmtClusterID", session.Spec.ManagementCluster.ResourceID)
		return 0, err
	}
	hcp, err := hcpprovider.GetHostedCluster(ctx, session.Spec.HostedControlPlane.Namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.V(2).Info("HostedCluster not found, will retry when it becomes available")
			session.MarkNetworkPathNotReady(sessiongatev1alpha1.ReasonHostedControlPlaneNotFound, "HostedControlPlane not yet available")
			session.Progressing(sessiongatev1alpha1.ReasonHostedControlPlaneNotFound, "Waiting for HostedControlPlane to be created")
			return 0, nil
		}
		logger.Error(err, "Failed to get HostedCluster")
		return 0, err
	}
	session.Status.BackendKASURL = fmt.Sprintf("https://%s", hcp.Spec.KubeAPIServerDNSName)
	session.MarkNetworkPathReady()

	// todo: this should be done based on a signal from the dataplane controller
	// when ALL pods registered the session
	session.MarkSessionActive()
	session.Status.Endpoint = c.registry.GetSessionEndpoint(session.Name)
	session.StopProgressing(sessiongatev1alpha1.ReasonAvailable, "Session is available")

	return timeUntilExpiration, nil
}

// enqueueSession extracts the key from the object and adds it to the workqueue for reconciliation
func (c *Controller) enqueueSession(obj interface{}) {
	// handle tombstones for Delete events
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}

	objectRef, err := cache.ObjectToName(obj)
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.Add(objectRef)
}

// enqueueOwningSession enqueues the owning Session of a resource
func (c *Controller) enqueueOwningSession(obj interface{}) {
	var object metav1.Object
	var ok bool
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object, invalid type"))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type"))
			return
		}
		c.logger.V(4).Info("Recovered deleted object", "resourceName", object.GetName())
	}

	c.logger.V(4).Info("Processing object", "object", klog.KObj(object))
	if ownerRef := metav1.GetControllerOf(object); ownerRef != nil {
		if ownerRef.Kind != "Session" {
			return
		}

		session, err := c.sessionsLister.Sessions(object.GetNamespace()).Get(ownerRef.Name)
		if err != nil {
			c.logger.V(4).Info("Ignoring orphaned object", "object", klog.KObj(object), "session", ownerRef.Name)
			return
		}

		c.enqueueSession(session)
		return
	}
}

// validateSession validates the session specification using Azure SDK parsing
func validateSession(session *sessiongatev1alpha1.Session) error {
	// validate TTL is positive
	if session.Spec.TTL.Duration <= 0 {
		return fmt.Errorf("spec.ttl must be a positive duration")
	}

	// validate managementCluster is provided
	if session.Spec.ManagementCluster.ResourceID == "" {
		return fmt.Errorf("spec.managementCluster.resourceId is required")
	}

	// validate managementCluster resource ID format and provider type
	mgmtResourceID, err := azcorearm.ParseResourceID(session.Spec.ManagementCluster.ResourceID)
	if err != nil {
		return fmt.Errorf("spec.managementCluster.resourceId is not a valid Azure resource ID: %w", err)
	}
	expectedMgmtProvider := "Microsoft.ContainerService"
	expectedMgmtType := "managedClusters"
	if !strings.EqualFold(mgmtResourceID.ResourceType.Namespace, expectedMgmtProvider) {
		return fmt.Errorf("spec.managementCluster must be a %s resource, got %s", expectedMgmtProvider, mgmtResourceID.ResourceType.Namespace)
	}
	if !strings.EqualFold(mgmtResourceID.ResourceType.Type, expectedMgmtType) {
		return fmt.Errorf("spec.managementCluster must be a %s/%s resource, got %s/%s",
			expectedMgmtProvider, expectedMgmtType,
			mgmtResourceID.ResourceType.Namespace, mgmtResourceID.ResourceType.Type)
	}

	// Validate hostedControlPlane is provided
	if session.Spec.HostedControlPlane.ResourceID == "" {
		return fmt.Errorf("spec.hostedControlPlane.resourceId is required")
	}

	// Validate hostedControlPlane resource ID format and provider type
	hcpResourceID, err := azcorearm.ParseResourceID(session.Spec.HostedControlPlane.ResourceID)
	if err != nil {
		return fmt.Errorf("spec.hostedControlPlane.resourceId is not a valid Azure resource ID: %w", err)
	}
	expectedHCPProvider := "Microsoft.RedHatOpenShift"
	expectedHCPType := "hcpOpenShiftClusters"
	if !strings.EqualFold(hcpResourceID.ResourceType.Namespace, expectedHCPProvider) {
		return fmt.Errorf("spec.hostedControlPlane must be a %s resource, got %s", expectedHCPProvider, hcpResourceID.ResourceType.Namespace)
	}
	if !strings.EqualFold(hcpResourceID.ResourceType.Type, expectedHCPType) {
		return fmt.Errorf("spec.hostedControlPlane must be a %s/%s resource, got %s/%s",
			expectedHCPProvider, expectedHCPType,
			hcpResourceID.ResourceType.Namespace, hcpResourceID.ResourceType.Type)
	}

	return nil
}

// patchSessionStatus patches the session status using SSA
func (c *Controller) patchSessionStatus(ctx context.Context, logger klog.Logger, session *sessiongatev1alpha1.Session) error {
	// Record the original resource version to detect if the update actually changed anything
	originalResourceVersion := session.ResourceVersion

	client := c.sessiongateclientset.SessiongateV1alpha1().Sessions(session.Namespace)

	// Build apply configuration for status
	statusApply := sessiongateapply.SessionStatus()

	// Apply conditions
	if len(session.Status.Conditions) > 0 {
		for _, cond := range session.Status.Conditions {
			statusApply = statusApply.WithConditions(
				metav1apply.Condition().
					WithType(cond.Type).
					WithStatus(cond.Status).
					WithObservedGeneration(cond.ObservedGeneration).
					WithLastTransitionTime(cond.LastTransitionTime).
					WithReason(cond.Reason).
					WithMessage(cond.Message),
			)
		}
	}

	// Apply other status fields
	if session.Status.ExpiresAt != nil {
		statusApply = statusApply.WithExpiresAt(*session.Status.ExpiresAt)
	}
	if session.Status.Endpoint != "" {
		statusApply = statusApply.WithEndpoint(session.Status.Endpoint)
	}
	if session.Status.CredentialsSecretRef != "" {
		statusApply = statusApply.WithCredentialsSecretRef(session.Status.CredentialsSecretRef)
	}
	if session.Status.BackendKASURL != "" {
		statusApply = statusApply.WithBackendKASURL(session.Status.BackendKASURL)
	}

	// Build Session apply configuration with just metadata and status
	sessionApply := sessiongateapply.Session(session.Name, session.Namespace).
		WithStatus(statusApply)

	updatedSession, err := client.ApplyStatus(
		ctx,
		sessionApply,
		metav1.ApplyOptions{FieldManager: controller.ControllerAgentName, Force: true},
	)
	if err != nil {
		logger.Error(err, "Failed to apply session status")
		return err
	}

	// Log whether the update was a no-op or actually changed something
	if updatedSession.ResourceVersion == originalResourceVersion {
		logger.V(6).Info("Status update was a no-op - no changes detected", "resourceVersion", updatedSession.ResourceVersion)
	} else {
		logger.V(6).Info("Applied session status - changes detected", "oldResourceVersion", originalResourceVersion, "newResourceVersion", updatedSession.ResourceVersion)
	}

	return nil
}
