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
	"path/filepath"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice"
	"github.com/Azure/kubelogin/pkg/cmd"
)

// GetAKSKubeconfig retrieves and configures a kubeconfig for an AKS cluster
func GetAKSKubeconfig(
	ctx context.Context,
	subscriptionID,
	resourceGroup,
	clusterName string,
	credential azcore.TokenCredential,
	kubeconfigPath string) error {
	kubeconfigContent, err := GetAKSKubeConfigContent(ctx, subscriptionID, resourceGroup, clusterName, credential)
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig content: %w", err)
	}
	// Open the kubeconfig file for writing (create or truncate)
	file, err := os.OpenFile(kubeconfigPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create kubeconfig file: %w", err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to close kubeconfig file: %v\n", cerr)
		}
	}()

	// Write kubeconfig content
	if _, err := file.Write(kubeconfigContent); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	// Run kubelogin to convert the kubeconfig using the library
	kubeloginCmd := cmd.NewRootCmd("hcpctl")
	kubeloginCmd.SetArgs([]string{"convert-kubeconfig", "-l", "azurecli", "--kubeconfig", kubeconfigPath})

	// Execute the kubelogin command
	if err := kubeloginCmd.ExecuteContext(ctx); err != nil {
		return fmt.Errorf("failed to convert kubeconfig with kubelogin: %w", err)
	}

	// Immediately update the kubeconfig to use our binary instead of standalone kubelogin
	if err := kubeloginifyKubeconfig(kubeconfigPath); err != nil {
		return fmt.Errorf("failed to update kubeconfig command: %w", err)
	}

	return nil
}

func GetAKSKubeConfigContent(
	ctx context.Context,
	subscriptionID,
	resourceGroup,
	clusterName string,
	credential azcore.TokenCredential) ([]byte, error) {
	// Create AKS client
	client, err := armcontainerservice.NewManagedClustersClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create AKS client: %w", err)
	}

	// Get the cluster access credentials
	resp, err := client.ListClusterUserCredentials(ctx, resourceGroup, clusterName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster access credentials: %w", err)
	}
	if len(resp.Kubeconfigs) == 0 {
		return nil, fmt.Errorf("no kubeconfig found")
	}
	kubeconfigContent := resp.Kubeconfigs[0].Value
	return kubeconfigContent, nil
}

// kubeloginifyKubeconfig updates the kubeconfig to use our binary with kubelogin subcommand
func kubeloginifyKubeconfig(kubeconfigPath string) error {
	// Load the kubeconfig
	config, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// Get the path to our binary
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Resolve any symlinks to get the real path
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %w", err)
	}

	// Update the kubeconfig using the exec path
	updateKubeconfigExecCommand(config, execPath)

	// Write the updated kubeconfig back
	if err := clientcmd.WriteToFile(*config, kubeconfigPath); err != nil {
		return fmt.Errorf("failed to write updated kubeconfig: %w", err)
	}

	return nil
}

// updateKubeconfigExecCommand updates the exec commands in the kubeconfig to use our binary
func updateKubeconfigExecCommand(config *clientcmdapi.Config, execPath string) {
	// Update all users' exec command
	for _, authInfo := range config.AuthInfos {
		if authInfo.Exec != nil && authInfo.Exec.Command == "kubelogin" {
			// Change command to use absolute path to our binary
			authInfo.Exec.Command = execPath

			// Prepend "kubelogin" to the args
			newArgs := []string{"kubelogin"}
			authInfo.Exec.Args = append(newArgs, authInfo.Exec.Args...)

			// Update the install hint
			binaryName := filepath.Base(execPath)
			authInfo.Exec.InstallHint = fmt.Sprintf("\n%s is not installed or not accessible.\n\nThe kubeconfig is configured to use: %s\n", binaryName, execPath)
		}
	}
}
