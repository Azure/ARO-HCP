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
	"fmt"
	"os"
	"os/exec"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/azauth"
)

func GetKubeConfig(ctx context.Context, subscriptionID, resourceGroupName, aksClusterName string) (string, error) {
	if aksClusterName == "" {
		return "", fmt.Errorf("AKSClusterName is required to build a kubeconfig")
	}

	// Create a new Azure identity client
	cred, err := azauth.GetAzureTokenCredentials()
	if err != nil {
		return "", fmt.Errorf("failed to obtain a credential: %v", err)
	}

	// Create a new AKS client
	client, err := armcontainerservice.NewManagedClustersClient(subscriptionID, cred, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create AKS client: %v", err)
	}

	// Get the cluster access credentials
	resp, err := client.ListClusterUserCredentials(ctx, resourceGroupName, aksClusterName, nil)
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
