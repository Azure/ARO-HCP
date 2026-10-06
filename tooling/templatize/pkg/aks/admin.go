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

package aks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	auth "github.com/microsoft/kiota-authentication-azure-go"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/Azure/ARO-Tools/tools/cmdutils"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
)

const (
	clusterAdminRoleID = "b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b" // Azure Kubernetes Service RBAC Cluster Admin
)

var errClusterAdminPermissionsDenied = errors.New("cluster admin permissions denied")

type ClusterAdminAssignmentOptions struct {
	Timeout        time.Duration
	CheckFrequency time.Duration
}

// EnsureClusterAdmin assigns the admin role only after an authorization denial,
// then waits for the same permission check to succeed.
func EnsureClusterAdmin(ctx context.Context, kubeconfigPath, subscriptionID, resourceGroupName, aksClusterName string, options *ClusterAdminAssignmentOptions) error {
	return ensureClusterAdmin(ctx, options, func(ctx context.Context) error {
		return CheckClusterAdminPermissions(ctx, kubeconfigPath)
	}, func(ctx context.Context) error {
		userObjectID, err := getCurrentUserObjectID(ctx)
		if err != nil {
			return fmt.Errorf("failed to get current user object ID: %w", err)
		}
		return assignClusterAdminRBACRole(ctx, subscriptionID, resourceGroupName, aksClusterName, userObjectID, clusterAdminRoleID)
	})
}

func ensureClusterAdmin(ctx context.Context, options *ClusterAdminAssignmentOptions, checkPermissions, assignRole func(context.Context) error) error {
	if options == nil {
		options = &ClusterAdminAssignmentOptions{
			Timeout:        time.Duration(2 * time.Minute),
			CheckFrequency: time.Duration(5 * time.Second),
		}
	}

	err := checkPermissions(ctx)
	if err == nil {
		return nil
	}
	if !errors.Is(err, errClusterAdminPermissionsDenied) {
		return fmt.Errorf("failed to check cluster admin permissions: %w", err)
	}

	err = assignRole(ctx)
	if err != nil {
		return fmt.Errorf("failed to assign cluster admin role: %w", err)
	}

	// Validate assignment
	err = checkPermissions(ctx)
	if err == nil {
		return nil
	}
	if !errors.Is(err, errClusterAdminPermissionsDenied) {
		return fmt.Errorf("failed to check cluster admin permissions: %w", err)
	}

	// Wait for role assignment to be effective
	fmt.Println("Wait for role assignment to be effective")
	timeoutTimer := time.NewTimer(options.Timeout)
	defer timeoutTimer.Stop()
	ticker := time.NewTicker(options.CheckFrequency)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeoutTimer.C:
			return fmt.Errorf("timed out waiting for role assignment to be effective: %w", err)
		case <-ticker.C:
			err = checkPermissions(ctx)
			if err == nil {
				fmt.Println("Cluster admin permissions are now effective")
				return nil
			}
			if !errors.Is(err, errClusterAdminPermissionsDenied) {
				return fmt.Errorf("failed to check cluster admin permissions: %w", err)
			}
			fmt.Println("Waiting for role assignment to be effective...")
		}
	}
}

// CheckClusterAdminPermissions reviews access to all resource verbs across API
// groups and namespaces without modifying workloads.
func CheckClusterAdminPermissions(ctx context.Context, kubeconfigPath string) error {
	clientset, err := createKubeClient(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	return checkClusterAdminPermissions(ctx, clientset)
}

func checkClusterAdminPermissions(ctx context.Context, clientset kubernetes.Interface) error {
	// Read access alone must not short-circuit the cluster admin assignment.
	review, err := clientset.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Group:    "*",
				Resource: "*",
				Verb:     "*",
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to review cluster admin permissions: %w", err)
	}
	if review.Status.EvaluationError != "" {
		return fmt.Errorf("failed to evaluate cluster admin permissions: %s", review.Status.EvaluationError)
	}
	if !review.Status.Allowed {
		return fmt.Errorf("%w: %s", errClusterAdminPermissionsDenied, review.Status.Reason)
	}
	return nil
}

func getCurrentUserObjectID(ctx context.Context) (string, error) {

	if os.Getenv("PRINCIPAL_ID") != "" {
		return os.Getenv("PRINCIPAL_ID"), nil
	}

	// Create a Graph client using Azure Credentials
	cred, err := cmdutils.GetAzureTokenCredentials()
	if err != nil {
		return "", fmt.Errorf("failed to obtain a credential: %w", err)
	}
	authProvider, err := auth.NewAzureIdentityAuthenticationProviderWithScopes(cred, []string{"https://graph.microsoft.com/.default"})
	if err != nil {
		return "", err
	}
	adapter, err := msgraphsdk.NewGraphRequestAdapter(authProvider)
	if err != nil {
		return "", err
	}
	client := msgraphsdk.NewGraphServiceClient(adapter)

	// Get the current user
	user, err := client.Me().Get(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get current user: %w", err)
	}

	// Extract the user ID
	userID := user.GetId()
	if userID == nil {
		return "", fmt.Errorf("user ID is nil")
	}

	return *userID, nil
}

func assignClusterAdminRBACRole(ctx context.Context, subscriptionID, resourceGroupName, aksClusterName, userObjectID, roleID string) error {
	// Create a new Azure identity client
	cred, err := cmdutils.GetAzureTokenCredentials()
	if err != nil {
		return fmt.Errorf("failed to obtain a credential: %w", err)
	}

	// Create a new role assignments client
	client, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create role assignments client: %w", err)
	}

	aksID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerService/managedClusters/%s", subscriptionID, resourceGroupName, aksClusterName)
	roleDefinitionID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", subscriptionID, roleID)

	// Define the role assignment parameters
	parameters := armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			RoleDefinitionID: to.Ptr(roleDefinitionID),
			PrincipalID:      to.Ptr(userObjectID),
		},
	}

	// Create the role assignment
	_, err = client.Create(ctx, aksID, uuid.New().String(), parameters, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.ErrorCode == "RoleAssignmentExists" {
			// we could check if the roleassignment exists upfront but even when
			// the role exists, checking for it is not always reliably detect it
			// so there is no point why we should check.
			return nil
		}
		return fmt.Errorf("failed to create role assignment: %w", err)
	}

	fmt.Println("Azure Kubernetes Service RBAC Cluster Admin role assignment created successfully")
	return nil
}

func createKubeClient(kubeconfigPath string) (*kubernetes.Clientset, error) {
	// Load the kubeconfig file
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig file: %w", err)
	}

	// Create the Kubernetes client
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	return clientset, nil
}
