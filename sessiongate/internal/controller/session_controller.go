/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sessiongatev1beta1 "github.com/Azure/ARO-HCP/sessiongate/api/v1beta1"
	"github.com/Azure/ARO-HCP/sessiongate/internal/mc"
	"github.com/Azure/ARO-HCP/sessiongate/internal/server"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	hypershiftclientset "github.com/openshift/hypershift/client/clientset/clientset"
)

const sessionFinalizer = "sessiongate.aro-hcp.azure.com/finalizer"

// SessionReconciler reconciles a Session object
type SessionReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	WebServer  *server.Server
	Credential azcore.TokenCredential
}

// +kubebuilder:rbac:groups=sessiongate.aro-hcp.azure.com,resources=sessions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sessiongate.aro-hcp.azure.com,resources=sessions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sessiongate.aro-hcp.azure.com,resources=sessions/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Session object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.1/pkg/reconcile
func (r *SessionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var session sessiongatev1beta1.Session
	if err := r.Get(ctx, req.NamespacedName, &session); err != nil {
		if errors.IsNotFound(err) {
			// Session was deleted, unregister from webserver
			r.WebServer.UnregisterSession(req.NamespacedName.String())
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !session.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&session, sessionFinalizer) {
			// Unregister session from webserver
			r.WebServer.UnregisterSession(session.Namespace + "/" + session.Name)

			// Remove finalizer
			controllerutil.RemoveFinalizer(&session, sessionFinalizer)
			if err := r.Update(ctx, &session); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&session, sessionFinalizer) {
		controllerutil.AddFinalizer(&session, sessionFinalizer)
		if err := r.Update(ctx, &session); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Calculate expiration time if not set
	var expiresAt *metav1.Time
	if session.Status.ExpiresAt == nil {
		now := metav1.Now()
		expirationTime := metav1.NewTime(now.Add(session.Spec.TTL.Duration))
		expiresAt = &expirationTime
	} else {
		expiresAt = session.Status.ExpiresAt
	}

	// Check if session has expired
	timeUntilExpiration := time.Until(expiresAt.Time)
	if timeUntilExpiration <= 0 {
		log.Info("Session has expired, deleting", "session", session.Name, "expiresAt", expiresAt.Time)
		if err := r.Delete(ctx, &session); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Determine which cluster to connect to
	var restConfig *rest.Config
	var targetCluster string

	if session.Spec.HostedControlPlane != "" {
		// HostedControlPlane is specified - discover the hosted cluster and get its kubeconfig
		log.Info("HostedControlPlane specified, discovering hosted cluster", "hcp", session.Spec.HostedControlPlane)

		// First get management cluster REST config
		mgmtConfig, err := mc.GetAKSRESTConfig(ctx, session.Spec.ManagementCluster, r.Credential)
		if err != nil {
			log.Error(err, "Failed to create REST config for management cluster")
			return ctrl.Result{}, err
		}

		// Create a controller-runtime client for the management cluster
		// Following HyperShift best practices: use controller-runtime client with proper scheme
		// Discover the hosted cluster using controller-runtime client
		hypershiftClient, err := hypershiftclientset.NewForConfig(mgmtConfig)
		hcpDiscovery := mc.NewDiscovery(hypershiftClient)
		resourceID, err := azcorearm.ParseResourceID(session.Spec.HostedControlPlane)
		if err != nil {
			log.Error(err, "Failed to parse hosted control plane resource ID")
			return ctrl.Result{}, err
		}
		hcpInfo, err := hcpDiscovery.DiscoverClusterByResourceID(ctx, resourceID)
		if err != nil {
			log.Error(err, "Failed to find hosted control plane")
			return ctrl.Result{}, err
		}

		// Get kubeconfig for the hosted cluster by minting a certificate
		// Use the access group as the user identifier and determine privilege level
		user := session.Spec.AccessLevel.Group
		privileged := true // TODO: determine based on access level
		restConfig, err = mc.GetHostedClusterRESTConfig(ctx, mgmtConfig, hcpInfo, user, privileged)
		if err != nil {
			log.Error(err, "Failed to get REST config for hosted cluster")
			return ctrl.Result{}, err
		}

		targetCluster = session.Spec.HostedControlPlane
		log.Info("Successfully configured session for hosted cluster", "hcp", session.Spec.HostedControlPlane)
	} else {
		// No HostedControlPlane - connect to management cluster
		log.Info("No HostedControlPlane specified, connecting to management cluster")
		var err error
		restConfig, err = mc.GetAKSRESTConfig(ctx, session.Spec.ManagementCluster, r.Credential)
		if err != nil {
			log.Error(err, "Failed to create REST config for management cluster")
			return ctrl.Result{}, err
		}
		targetCluster = session.Spec.ManagementCluster
	}

	// Register or update session in server
	sessionID := session.Namespace + "/" + session.Name
	endpoint, err := r.WebServer.RegisterSession(server.SessionOptions{
		SessionID:          sessionID,
		ExpiresAt:          expiresAt,
		ManagementCluster:  session.Spec.ManagementCluster,
		HostedControlPlane: session.Spec.HostedControlPlane,
		AccessGroup:        session.Spec.AccessLevel.Group,
		RESTConfig:         restConfig,
	})
	if err != nil {
		log.Error(err, "Failed to register session")
		return ctrl.Result{}, err
	}

	log.Info("Session registered successfully", "sessionID", sessionID, "targetCluster", targetCluster, "endpoint", endpoint)

	// Update status if needed
	needsStatusUpdate := false
	if session.Status.Endpoint != endpoint {
		session.Status.Endpoint = endpoint
		needsStatusUpdate = true
	}
	if session.Status.ExpiresAt == nil {
		session.Status.ExpiresAt = expiresAt
		needsStatusUpdate = true
	}

	if needsStatusUpdate {
		if err := r.Status().Update(ctx, &session); err != nil {
			log.Error(err, "Failed to update Session status")
			return ctrl.Result{}, err
		}
	}

	// Requeue when session expires
	return ctrl.Result{RequeueAfter: timeUntilExpiration}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SessionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sessiongatev1beta1.Session{}).
		Named("session").
		Complete(r)
}
