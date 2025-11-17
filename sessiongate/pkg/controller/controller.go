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
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
	istioclientset "istio.io/client-go/pkg/clientset/versioned/typed/security/v1beta1"
	istioinformers "istio.io/client-go/pkg/informers/externalversions/security/v1beta1"
	istiolisters "istio.io/client-go/pkg/listers/security/v1beta1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	clientset "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/clientset/versioned"
	sessiongateschema "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/clientset/versioned/scheme"
	informers "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/informers/externalversions/sessiongate/v1alpha1"
	listers "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/listers/sessiongate/v1alpha1"
)

// ControllerAgentName is the name of the controller agent
const ControllerAgentName = "sessiongate-controller"

const controllerAgentName = ControllerAgentName

const (
	// SuccessSynced is used as part of the Event 'reason' when a Session is synced
	SuccessSynced = "Synced"

	// MessageResourceSynced is the message used for an Event fired when a Session
	// is synced successfully
	MessageResourceSynced = "Session synced successfully"

	// FieldManager distinguishes this controller from other things writing to API objects
	FieldManager = controllerAgentName

	// sessionFinalizer is the finalizer name used for cleanup
	sessionFinalizer = "sessiongate.aro-hcp.azure.com/finalizer"

	// LabelManagedBy identifies resources managed by the sessiongate controller
	LabelManagedBy = "app.kubernetes.io/managed-by"
)

// ManagedByLabelSelector returns a label selector string for resources managed by this controller
// This is used to filter informers to only watch resources created and managed by sessiongate-controller
func ManagedByLabelSelector() string {
	return fmt.Sprintf("%s=%s", LabelManagedBy, ControllerAgentName)
}

// LeaderElectionConfig holds configuration for leader election
type LeaderElectionConfig struct {
	LockName      string
	LeaseDuration time.Duration
	RenewDeadline time.Duration
	RetryPeriod   time.Duration
	Namespace     string
	KubeConfig    *rest.Config
}

// Controller is the controller implementation for Session resources
type Controller struct {
	kubeclientset        kubernetes.Interface
	sessiongateclientset clientset.Interface
	istioclientset       istioclientset.SecurityV1beta1Interface
	sessionsLister       listers.SessionLister
	sessionsSynced       cache.InformerSynced
	authzPoliciesLister  istiolisters.AuthorizationPolicyLister
	authzPoliciesSynced  cache.InformerSynced
	workqueue            workqueue.TypedRateLimitingInterface[cache.ObjectName]
	registry             SessionRegistry
	credentialProvider   CredentialProvider
	credential           azcore.TokenCredential
	sessionNamespace     string
	secretsLister        corev1listers.SecretLister
	secretsSynced        cache.InformerSynced
	isLeader             *atomic.Bool
	leaderElectionConfig *LeaderElectionConfig
}

func NewController(
	ctx context.Context,
	kubeclientset kubernetes.Interface,
	sessiongateclientset clientset.Interface,
	istioclientset istioclientset.SecurityV1beta1Interface,
	sessionsInformer informers.SessionInformer,
	authzPolicyInformer istioinformers.AuthorizationPolicyInformer,
	secretsInformer cache.SharedIndexInformer,
	registry SessionRegistry,
	credentialProvider CredentialProvider,
	credential azcore.TokenCredential,
	sessionNamespace string,
	leaderElectionConfig *LeaderElectionConfig) (*Controller, error) {
	logger := klog.FromContext(ctx)

	utilruntime.Must(sessiongateschema.AddToScheme(scheme.Scheme))

	ratelimiter := workqueue.NewTypedMaxOfRateLimiter(
		workqueue.NewTypedItemExponentialFailureRateLimiter[cache.ObjectName](5*time.Millisecond, 1000*time.Second),
		&workqueue.TypedBucketRateLimiter[cache.ObjectName]{Limiter: rate.NewLimiter(rate.Limit(50), 300)},
	)

	controller := &Controller{
		kubeclientset:        kubeclientset,
		sessiongateclientset: sessiongateclientset,
		istioclientset:       istioclientset,
		sessionsLister:       sessionsInformer.Lister(),
		sessionsSynced:       sessionsInformer.Informer().HasSynced,
		authzPoliciesLister:  authzPolicyInformer.Lister(),
		authzPoliciesSynced:  authzPolicyInformer.Informer().HasSynced,
		workqueue:            workqueue.NewTypedRateLimitingQueue(ratelimiter),
		registry:             registry,
		credentialProvider:   credentialProvider,
		credential:           credential,
		sessionNamespace:     sessionNamespace,
		secretsLister:        corev1listers.NewSecretLister(secretsInformer.GetIndexer()),
		secretsSynced:        secretsInformer.HasSynced,
		isLeader:             &atomic.Bool{},
		leaderElectionConfig: leaderElectionConfig,
	}

	logger.Info("Setting up event handlers")

	// Session Informer for control plane
	// enqueues Sessions for reconciliation
	if _, err := sessionsInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueSession,
		UpdateFunc: func(old, new interface{}) {
			controller.enqueueSession(new)
		},
		DeleteFunc: controller.enqueueSession,
	}); err != nil {
		return nil, fmt.Errorf("failed to add event handler for sessions (control plane): %w", err)
	}

	// Session Informer for shared data plane
	// registers sessions with KAS reverse proxy when credentials are ready
	if _, err := sessionsInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.handleSessionRegistration,
		UpdateFunc: func(old, new interface{}) {
			controller.handleSessionRegistration(new)
		},
		DeleteFunc: controller.handleSessionRegistration,
	}); err != nil {
		return nil, fmt.Errorf("failed to add event handler for sessions (data plane): %w", err)
	}

	// Secret Informer for control plane
	// drift detection (mostly deletions of secrets outside of Session lifecycle)
	if _, err := secretsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueOwningSession,
		UpdateFunc: func(old, new interface{}) {
			newSecret := new.(*metav1.ObjectMeta)
			oldSecret := old.(*metav1.ObjectMeta)
			if newSecret.ResourceVersion == oldSecret.ResourceVersion {
				return
			}
			controller.enqueueOwningSession(new)
		},
		DeleteFunc: func(obj interface{}) {
			controller.enqueueOwningSession(obj)
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to add event handler for secrets: %w", err)
	}

	// AuthorizationPolicy Informer for control plane
	// drift detection (mostly deletions of policies outside of Session lifecycle)
	if _, err := authzPolicyInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueOwningSession,
		UpdateFunc: func(old, new interface{}) {
			newPolicy := new.(*metav1.ObjectMeta)
			oldPolicy := old.(*metav1.ObjectMeta)
			if newPolicy.ResourceVersion == oldPolicy.ResourceVersion {
				return
			}
			controller.enqueueOwningSession(new)
		},
		DeleteFunc: controller.enqueueOwningSession,
	}); err != nil {
		return nil, fmt.Errorf("failed to add event handler for authorization policies: %w", err)
	}

	return controller, nil
}

// participates in leader election and runs controller workers when elected leader
func (c *Controller) Run(ctx context.Context, workers int) error {
	logger := klog.FromContext(ctx)

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

	logger.Info("Leader election configured",
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
				c.isLeader.Store(true)
				logger.Info("Acquired leadership - starting controller workers")

				if err := c.run(leaderCtx, workers); err != nil {
					logger.Error(err, "Controller stopped with error")
				}
			},
			OnStoppedLeading: func() {
				c.isLeader.Store(false)
				logger.Info("Lost leadership - workers stopped")
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create leader elector: %w", err)
	}

	logger.Info("Starting leader election")
	le.Run(ctx)
	return nil
}

// start the controller workers and blocks until the context is cancelled
func (c *Controller) run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()
	logger := klog.FromContext(ctx)

	// Start the informer factories to begin populating the informer caches
	logger.Info("Starting Session controller")
	logger.Info("Waiting for informer caches to sync")

	if ok := cache.WaitForCacheSync(ctx.Done(), c.sessionsSynced, c.secretsSynced, c.authzPoliciesSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	logger.Info("Enqueuing all Sessions for initial reconciliation")
	sessions, err := c.sessionsLister.Sessions(c.sessionNamespace).List(labels.Everything())
	if err != nil {
		return fmt.Errorf("failed to list sessions for initial reconciliation: %w", err)
	}
	for _, session := range sessions {
		c.enqueueSession(session)
	}
	logger.Info("Enqueued Sessions for reconciliation", "count", len(sessions))

	logger.Info("Starting workers", "count", workers)
	for range workers {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	logger.Info("Started workers")
	<-ctx.Done()
	logger.Info("Shutting down workers")

	return nil
}

// runWorker continually calls processNextWorkItem to read and process messages on the workqueue
func (c *Controller) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem reads a single work item off the workqueue and attempts to process it, by calling the syncHandler
// returns true if the item was processed successfully, false if the workqueue should be shut down
// handles requeues and retries
func (c *Controller) processNextWorkItem(ctx context.Context) bool {
	objRef, shutdown := c.workqueue.Get()
	logger := klog.FromContext(ctx)

	if shutdown {
		return false
	}

	defer c.workqueue.Done(objRef)

	requeueAfter, err := c.syncHandler(ctx, objRef)
	if err == nil {
		c.workqueue.Forget(objRef)
		logger.V(6).Info("Successfully synced", "objectName", objRef)

		if requeueAfter > 0 {
			c.workqueue.AddAfter(objRef, requeueAfter)
		}
		return true
	}
	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", objRef)
	c.workqueue.AddRateLimited(objRef)
	return true
}

// reconciles a session to desired state and returns requeue duration (0 means no requeue)
func (c *Controller) syncHandler(ctx context.Context, objectRef cache.ObjectName) (time.Duration, error) {
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "objectRef", objectRef)

	session, err := c.sessionsLister.Sessions(objectRef.Namespace).Get(objectRef.Name)
	if err != nil {
		// session does not exist or no longer exists, stop processing and unregister from registry
		if errors.IsNotFound(err) {
			logger.Info("Session no longer exists, unregistering from registry")
			c.registry.UnregisterSession(objectRef.Name)
			return 0, nil
		}

		return 0, err
	}

	// hard lesson learned: only operate on a copy of the cached session object
	sessionCopy := session.DeepCopy()

	//
	// Phase 0: handle delete before everything else
	//

	// deletion
	if !session.DeletionTimestamp.IsZero() {
		return c.handleDeletion(ctx, session)
	}

	//
	// Phase 1: initialization, expiration, validation
	//

	// initialize conditions
	initializeConditions(sessionCopy)
	defer func() {
		if _, updateErr := c.sessiongateclientset.SessiongateV1alpha1().Sessions(sessionCopy.Namespace).UpdateStatus(ctx, sessionCopy, metav1.UpdateOptions{FieldManager: FieldManager}); updateErr != nil {
			logger.Error(updateErr, "Failed to update session status")
		}
	}()

	// finalizer
	if !containsFinalizer(session, sessionFinalizer) {
		if err := c.addFinalizer(ctx, session, sessionFinalizer); err != nil {
			logger.Error(err, "Failed to add finalizer to Session")
			return 0, err
		}
		logger.V(2).Info("Added finalizer to Session", "session", klog.KObj(session))
		// Return early to let the update event trigger a new reconcile
		return 0, nil
	}

	// calculate expiration time if not set
	var expiresAt *metav1.Time
	if session.Status.ExpiresAt == nil {
		now := metav1.Now()
		expirationTime := metav1.NewTime(now.Add(session.Spec.TTL.Duration))
		expiresAt = &expirationTime
	} else {
		expiresAt = session.Status.ExpiresAt
	}
	sessionCopy.Status.ExpiresAt = expiresAt

	// check for expiration
	timeUntilExpiration := time.Until(expiresAt.Time)
	if timeUntilExpiration <= 0 {
		logger.Info("Session has expired, deleting", "session", session.Name, "expiresAt", expiresAt.Time)
		setExpiredCondition(sessionCopy)
		if err := c.sessiongateclientset.SessiongateV1alpha1().Sessions(session.Namespace).Delete(ctx, session.Name, metav1.DeleteOptions{}); err != nil {
			return 0, err
		}
		return 0, nil
	}

	// validation
	setProgressingCondition(sessionCopy, ReasonInitializing, "Validating session specification")
	if err := validateSession(session); err != nil {
		setDegradedCondition(sessionCopy, ReasonInvalidConfiguration, err.Error())
		logger.Error(err, "Session has invalid configuration")
		return 0, nil
	}
	clearDegradedCondition(sessionCopy)

	//
	// Phase 2: authorization policy
	//

	setProgressingCondition(sessionCopy, ReasonConfiguringAuthorization, "Ensuring authorization policy")
	changed, err := c.ensureAuthorizationPolicy(ctx, session)
	if err != nil {
		logger.Error(err, "Failed to ensure AuthorizationPolicy")
		setTransientError(sessionCopy, ConditionTypeAvailable, ReasonAuthorizationFailed, fmt.Sprintf("Failed to ensure authorization policy: %v", err))
		return 0, err
	}
	if changed {
		logger.V(2).Info("AuthorizationPolicy changed, waiting for informer notification")
		return 0, nil
	}

	//
	// Phase 3: credential provisioning
	//

	setProgressingCondition(sessionCopy, ReasonMintingCredentials, "Provisioning cluster credentials")
	credStatus, secretName, backendKASURL, csrName, err := c.credentialProvider.EnsureCredentials(ctx, session)
	if err != nil {
		logger.Error(err, "Failed to provision credentials")
		setTransientError(sessionCopy, ConditionTypeAvailable, ReasonCredentialMintingFailed, fmt.Sprintf("Failed to provision credentials: %v", err))
		setCredentialsCondition(sessionCopy, false, ReasonCredentialsFailed, fmt.Sprintf("Credential provisioning failed: %v", err))
		return 0, err
	}

	sessionCopy.Status.CredentialsSecretRef = &corev1.LocalObjectReference{Name: secretName}
	sessionCopy.Status.BackendKASURL = backendKASURL
	sessionCopy.Status.CSRName = csrName

	// handle credential status and requeue as needed
	switch credStatus {
	case CredentialStatusPrivateKeyCreated:
		logger.V(2).Info("Private key created", "secretName", secretName)
		setCredentialsCondition(sessionCopy, false, ReasonPrivateKeyCreated, "Private key generated, preparing certificate request")
		return time.Millisecond, nil

	case CredentialStatusCertificatePending:
		logger.V(2).Info("Certificate pending, requeueing to poll", "secretName", secretName, "csrName", csrName)
		setCredentialsCondition(sessionCopy, false, ReasonCertificatePending, fmt.Sprintf("Certificate signing request %s submitted, waiting for approval", csrName))
		return 2 * time.Second, nil

	case CredentialStatusReady:
		logger.V(2).Info("Credentials ready", "secretName", secretName)
		setCredentialsCondition(sessionCopy, true, ReasonCredentialsReady, "Credentials are ready")
		// clear CSR name from status, no longer needed
		sessionCopy.Status.CSRName = ""

	default:
		logger.Error(nil, "Unknown credential status", "status", credStatus)
		setTransientError(sessionCopy, ConditionTypeAvailable, ReasonCredentialMintingFailed, "Unknown credential status")
		return 0, fmt.Errorf("unknown credential status: %d", credStatus)
	}

	//
	// Phase 4: endpoint publishing
	//

	setProgressingCondition(sessionCopy, ReasonPublishingEndpoint, "Registering session with proxy")
	endpoint := c.registry.GetSessionEndpoint(session.Name)
	sessionCopy.Status.Endpoint = endpoint

	//
	// Phase 5: success - mark as ready
	//

	setAvailableCondition(sessionCopy, true, ReasonEndpointPublished, fmt.Sprintf("Endpoint published at %s", endpoint))
	setReadyCondition(sessionCopy, true, ReasonSessionReady, "Session is fully operational")

	return timeUntilExpiration, nil
}

// handleDeletion handles the deletion of a Session resource
func (c *Controller) handleDeletion(ctx context.Context, session *sessiongatev1alpha1.Session) (time.Duration, error) {
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "session", klog.KObj(session))

	if containsFinalizer(session, sessionFinalizer) {
		if err := c.removeFinalizer(ctx, session, sessionFinalizer); err != nil {
			return 0, err
		}
		logger.Info("Removed finalizer from session", "sessionID", session.Name)
	}
	return 0, nil
}

// enqueueSession takes a Session resource and converts it into a namespace/name
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than Session.
func (c *Controller) enqueueSession(obj interface{}) {
	// Only enqueue if this controller is the elected leader
	if !c.isLeader.Load() {
		klog.V(4).Info("Skipping Session enqueue - not leader")
		return
	}

	if objectRef, err := cache.ObjectToName(obj); err != nil {
		utilruntime.HandleError(err)
		return
	} else {
		c.workqueue.Add(objectRef)
	}
}

// handleSessionRegistration run on all controller instances to register a ready session in the shared dataplane.
func (c *Controller) handleSessionRegistration(obj interface{}) {
	var object metav1.Object
	var ok bool
	logger := klog.Background()

	// Handle both live objects and tombstones
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			klog.ErrorS(nil, "Error decoding object, invalid type", "type", fmt.Sprintf("%T", obj))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			klog.ErrorS(nil, "Error decoding object tombstone, invalid type", "type", fmt.Sprintf("%T", tombstone.Obj))
			return
		}
		klog.V(4).InfoS("Recovered deleted object", "resourceName", object.GetName())
	}

	session, ok := object.(*sessiongatev1alpha1.Session)
	if !ok {
		klog.ErrorS(nil, "Error decoding Session object, invalid type", "type", fmt.Sprintf("%T", object))
		return
	}

	logger = logger.WithValues("session", klog.KObj(session))

	// Handle deletion - unregister from local registry
	if !session.DeletionTimestamp.IsZero() {
		c.registry.UnregisterSession(session.Name)
		logger.Info("Unregistered session from local registry")
		return
	}

	// Check if credentials are ready via condition
	if !areCredentialsReady(session) {
		logger.V(4).Info("Credentials not ready, skipping registration")
		return
	}

	// Get Secret name from status
	if session.Status.CredentialsSecretRef == nil {
		logger.V(4).Info("No credentials secret reference, skipping registration")
		return
	}

	// Get backend KAS URL from status
	backendKASURL := session.Status.BackendKASURL
	if backendKASURL == "" {
		logger.V(4).Info("No backend KAS URL, skipping registration")
		return
	}

	// Get Secret from lister (cached, no API call)
	secret, err := c.secretsLister.Secrets(c.sessionNamespace).Get(session.Status.CredentialsSecretRef.Name)
	if err != nil {
		logger.Error(err, "Failed to get credentials secret from lister")
		return
	}

	// Get credentials from Secret using credential provider
	restConfig, _, err := c.credentialProvider.GetCredentialsFromSecret(context.Background(), secret.Namespace, secret.Name)
	if err != nil {
		logger.Error(err, "Failed to get credentials from secret")
		return
	}

	// Register session in local data plane
	endpoint, err := c.registry.RegisterSession(NewSessionOptions(
		session.Name,
		backendKASURL,
		restConfig,
	))
	if err != nil {
		logger.Error(err, "Failed to register session")
		return
	}

	logger.Info("Registered session in local registry",
		"endpoint", endpoint,
		"backendKASURL", backendKASURL)
}

// addFinalizer adds a finalizer to a Session using Merge Patch
func (c *Controller) addFinalizer(ctx context.Context, session *sessiongatev1alpha1.Session, finalizer string) error {
	// Build new finalizer list with the specified finalizer
	newFinalizers := append(session.Finalizers, finalizer)

	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"finalizers": newFinalizers,
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	_, err = c.sessiongateclientset.SessiongateV1alpha1().Sessions(session.Namespace).Patch(
		ctx,
		session.Name,
		types.MergePatchType,
		patchBytes,
		metav1.PatchOptions{FieldManager: FieldManager},
	)
	return err
}

// removeFinalizer removes all finalizers from a Session using Merge Patch
func (c *Controller) removeFinalizer(ctx context.Context, session *sessiongatev1alpha1.Session, finalizer string) error {
	// Build new finalizer list without the specified finalizer
	newFinalizers := []string{}
	for _, f := range session.Finalizers {
		if f != finalizer {
			newFinalizers = append(newFinalizers, f)
		}
	}

	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"finalizers": newFinalizers,
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	_, err = c.sessiongateclientset.SessiongateV1alpha1().Sessions(session.Namespace).Patch(
		ctx,
		session.Name,
		types.MergePatchType,
		patchBytes,
		metav1.PatchOptions{FieldManager: FieldManager},
	)
	return err
}

// validateSession validates the session specification using Azure SDK parsing
func validateSession(session *sessiongatev1alpha1.Session) error {
	// Validate managementCluster is provided
	if session.Spec.ManagementCluster == "" {
		return fmt.Errorf("spec.managementCluster is required")
	}

	// Validate managementCluster resource ID format and provider type
	mgmtResourceID, err := azcorearm.ParseResourceID(session.Spec.ManagementCluster)
	if err != nil {
		return fmt.Errorf("spec.managementCluster is not a valid Azure resource ID: %w", err)
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

	// Validate hostedControlPlane resource ID format and provider type (if provided)
	if session.Spec.HostedControlPlane != "" {
		hcpResourceID, err := azcorearm.ParseResourceID(session.Spec.HostedControlPlane)
		if err != nil {
			return fmt.Errorf("spec.hostedControlPlane is not a valid Azure resource ID: %w", err)
		}
		if !strings.EqualFold(hcpResourceID.ResourceType.Namespace, "Microsoft.RedHatOpenShift") {
			return fmt.Errorf("spec.hostedControlPlane must be a %s resource, got %s", "Microsoft.RedHatOpenShift", hcpResourceID.ResourceType.Namespace)
		}
		if !strings.EqualFold(hcpResourceID.ResourceType.Type, "hcpOpenShiftClusters") {
			return fmt.Errorf("spec.hostedControlPlane must be a %s/%s resource, got %s/%s",
				"Microsoft.RedHatOpenShift", "hcpOpenShiftClusters",
				hcpResourceID.ResourceType.Namespace, hcpResourceID.ResourceType.Type)
		}
	}

	// Validate owner fields
	if session.Spec.Owner.UserPrincipal == nil || session.Spec.Owner.UserPrincipal.Claim == "" || session.Spec.Owner.UserPrincipal.Name == "" {
		return fmt.Errorf("spec.owner.userPrincipal.claim and spec.owner.userPrincipal.name are required")
	}

	return nil
}

// containsFinalizer checks if a finalizer exists in the list
func containsFinalizer(session *sessiongatev1alpha1.Session, finalizer string) bool {
	for _, f := range session.Finalizers {
		if f == finalizer {
			return true
		}
	}
	return false
}

func (c *Controller) enqueueOwningSession(obj interface{}) {
	logger := klog.Background()

	if !c.isLeader.Load() {
		logger.V(4).Info("Skipping Secret event - not leader")
		return
	}

	var object metav1.Object
	var ok bool

	// Handle both live objects and tombstones (for Delete events)
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			klog.ErrorS(nil, "Error decoding object, invalid type", "type", fmt.Sprintf("%T", obj))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			klog.ErrorS(nil, "Error decoding object tombstone, invalid type", "type", fmt.Sprintf("%T", tombstone.Obj))
			return
		}
		logger.V(4).Info("Recovered deleted object from tombstone", "resourceName", object.GetName())
	}

	// Get the owning Session using owner reference
	ownerRef := metav1.GetControllerOf(object)
	if ownerRef == nil {
		logger.V(4).Info("Secret has no controller owner, ignoring")
		return
	}

	// Only handle Secrets owned by Sessions
	if ownerRef.Kind != "Session" {
		logger.V(4).Info("Secret is not owned by a Session, ignoring", "ownerKind", ownerRef.Kind)
		return
	}

	c.enqueueSession(ownerRef.Name)
}
