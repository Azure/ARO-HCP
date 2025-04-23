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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armauthorization "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/google/uuid"
	auth "github.com/microsoft/kiota-authentication-azure-go"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/azauth"
)

const (
	clusterAdminRoleID = "b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b" // Azure Kubernetes Service RBAC Cluster Admin
)

type ClusterAdminAssignmentOptions struct {
	Timeout        time.Duration
	CheckFrequency time.Duration
}

func EnsureClusterAdmin(ctx context.Context, kubeconfigPath, subscriptionID, resourceGroupName, aksClusterName string, options *ClusterAdminAssignmentOptions) error {
	if options == nil {
		options = &ClusterAdminAssignmentOptions{
			Timeout:        time.Duration(2 * time.Minute),
			CheckFrequency: time.Duration(5 * time.Second),
		}
	}

	// Get the current user's object ID
	userObjectID, err := getCurrentUserObjectID(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current user object ID: %w", err)
	}

	// Assign the Azure Kubernetes Service RBAC Cluster Admin role to the current user
	err = assignClusterAdminRBACRole(ctx, subscriptionID, resourceGroupName, aksClusterName, userObjectID, clusterAdminRoleID)
	if err != nil {
		return fmt.Errorf("failed to assign cluster admin role: %w", err)
	}

	// Validate assignment
	err = CheckClusterAdminPermissions(ctx, kubeconfigPath)
	if err == nil {
		return nil
	}

	// Wait for role assignment to be effective
	fmt.Println("Wait for role assignment to be effective")
	timeout := time.After(options.Timeout)
	ticker := time.NewTicker(options.CheckFrequency)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("timed out waiting for role assignment to be effective")
		case <-ticker.C:
			err = CheckClusterAdminPermissions(ctx, kubeconfigPath)
			if err == nil {
				fmt.Println("Cluster admin permissions are now effective")
				return nil
			}
			fmt.Println("Waiting for role assignment to be effective...")
		}
	}
}

func CheckClusterAdminPermissions(ctx context.Context, kubeconfigPath string) error {
	clientset, err := createKubeClient(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Implement the logic to test cluster admin permissions
	// by checking if the user can list pods in the default namespace
	_, err = clientset.CoreV1().Pods("default").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list pods in the default namespace: %w", err)
	}
	return nil
}

func getCurrentUserObjectID(ctx context.Context) (string, error) {

	if os.Getenv("PRINCIPAL_ID") != "" {
		return os.Getenv("PRINCIPAL_ID"), nil
	}

	// Create a Graph client using Azure Credentials
	cred, err := azauth.GetAzureTokenCredentials()
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
	cred, err := azauth.GetAzureTokenCredentials()
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
			// so there is no point why we should check. all our users have
			// permissions to create such role assignments anyways
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
