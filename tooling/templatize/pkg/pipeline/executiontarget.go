package pipeline

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
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
	AKSClusterName   string
}

func (target *ExecutionTarget) KubeConfig(ctx context.Context) (string, error) {
	if target.AKSClusterName == "" {
		return "", fmt.Errorf("AKSClusterName is required to build a kubeconfig")
	}

	// Create a new Azure identity client
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return "", fmt.Errorf("failed to obtain a credential: %v", err)
	}

	// Create a new AKS client
	client, err := armcontainerservice.NewManagedClustersClient(target.SubscriptionID, cred, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create AKS client: %v", err)
	}

	// Get the cluster access credentials
	resp, err := client.ListClusterUserCredentials(ctx, target.ResourceGroup, target.AKSClusterName, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get cluster access credentials: %v", err)
	}
	if len(resp.Kubeconfigs) == 0 {
		return "", fmt.Errorf("no kubeconfig found")
	}
	kubeconfigContent := resp.Kubeconfigs[0].Value

	// store the kubeconfig content into a temporary file
	// generate a unique temporary filename
	tmpfile, err := os.CreateTemp("", "kubeconfig-*.yaml")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file for kubeconfig: %v", err)
	}
	defer tmpfile.Close()

	// store the kubeconfig content into the temporary file
	if _, err := tmpfile.Write([]byte(kubeconfigContent)); err != nil {
		return "", fmt.Errorf("failed to write to temporary kubeconfigfile %s: %v", tmpfile.Name(), err)
	}

	// Run kubelogin to transform the kubeconfig
	cmd := exec.CommandContext(ctx, "kubelogin", "convert-kubeconfig", "-l", "azurecli", "--kubeconfig", tmpfile.Name())
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to run kubelogin: %s %v", string(output), err)
	}

	return tmpfile.Name(), nil
}

func (target *ExecutionTarget) Name() string {
	return fmt.Sprintf("SUB %s / RG %s / AKS %s", target.SubscriptionName, target.ResourceGroup, target.AKSClusterName)
}
