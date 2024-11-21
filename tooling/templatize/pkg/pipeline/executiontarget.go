package pipeline

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/aks"
)

func lookupSubscriptionID(ctx context.Context, subscriptionName string) (string, error) {
	// Create a new Azure identity client
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return "", fmt.Errorf("failed to obtain a credential: %v", err)
	}

	// Create a new subscriptions client
	client, err := armsubscriptions.NewClient(cred, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create subscriptions client: %v", err)
	}

	// List subscriptions and find the one with the matching name
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to get next page of subscriptions: %v", err)
		}
		for _, sub := range page.Value {
			if sub.DisplayName != nil && *sub.DisplayName == subscriptionName {
				return *sub.SubscriptionID, nil
			}
		}
	}

	return "", fmt.Errorf("subscription with name %q not found", subscriptionName)
}

type ExecutionTarget struct {
	SubscriptionName string
	SubscriptionID   string
	ResourceGroup    string
	Region           string
	AKSClusterName   string
}

func (target *ExecutionTarget) KubeConfig(ctx context.Context) (string, error) {
	if target.AKSClusterName == "" {
		return "", fmt.Errorf("AKS cluster name is required to build a kubeconfig")
	}

	// Get Kubeconfig
	kubeconfigPath, err := aks.GetKubeConfig(ctx, target.SubscriptionID, target.ResourceGroup, target.AKSClusterName)
	if err != nil {
		return "", fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	// Make sure we have cluster admin
	err = aks.EnsureClusterAdmin(ctx, kubeconfigPath, target.SubscriptionID, target.ResourceGroup, target.AKSClusterName, nil)
	if err != nil {
		return "", fmt.Errorf("failed to ensure cluster admin role: %w", err)
	}
	return kubeconfigPath, nil
}

func (target *ExecutionTarget) aksID() string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerService/managedClusters/%s", target.SubscriptionID, target.ResourceGroup, target.AKSClusterName)
}
