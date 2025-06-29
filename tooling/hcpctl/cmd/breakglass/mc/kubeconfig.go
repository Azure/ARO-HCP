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

package mc

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice"
	"github.com/Azure/kubelogin/pkg/cmd"
	"k8s.io/client-go/tools/clientcmd"
)

// GetAKSKubeconfig retrieves the kubeconfig for an AKS cluster
func GetAKSKubeconfig(ctx context.Context, opts *CompletedMCOptions) (string, error) {
	// Create AKS client
	client, err := armcontainerservice.NewManagedClustersClient(opts.SubscriptionID, opts.AzureCredential, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create AKS client: %w", err)
	}

	// Get the cluster access credentials
	resp, err := client.ListClusterUserCredentials(ctx, opts.ResourceGroup, opts.ClusterName, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get cluster access credentials: %w", err)
	}
	if len(resp.Kubeconfigs) == 0 {
		return "", fmt.Errorf("no kubeconfig found")
	}
	kubeconfigContent := resp.Kubeconfigs[0].Value

	// Create temporary file for kubeconfig
	tmpfile, err := os.CreateTemp("", "mc-kubeconfig-*.yaml")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file for kubeconfig: %w", err)
	}
	defer tmpfile.Close()

	// Write kubeconfig content
	if _, err := tmpfile.Write(kubeconfigContent); err != nil {
		return "", fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	// Run kubelogin to convert the kubeconfig using the library
	kubeloginCmd := cmd.NewRootCmd("hcpctl")
	kubeloginCmd.SetArgs([]string{"convert-kubeconfig", "-l", "azurecli", "--kubeconfig", tmpfile.Name()})

	// Execute the kubelogin command
	if err := kubeloginCmd.ExecuteContext(ctx); err != nil {
		return "", fmt.Errorf("failed to convert kubeconfig with kubelogin: %w", err)
	}

	// Update the kubeconfig to use our binary instead of standalone kubelogin
	if err := updateKubeconfigCommand(tmpfile.Name()); err != nil {
		return "", fmt.Errorf("failed to update kubeconfig command: %w", err)
	}

	return tmpfile.Name(), nil
}

// copyKubeconfig copies the kubeconfig file to the specified output path
func copyKubeconfig(source, dest string) error {
	sourceFile, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("failed to open source kubeconfig: %w", err)
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return fmt.Errorf("failed to copy kubeconfig: %w", err)
	}

	// Set appropriate permissions
	if err := destFile.Chmod(0600); err != nil {
		return fmt.Errorf("failed to set kubeconfig permissions: %w", err)
	}

	return nil
}

// updateKubeconfigCommand updates the kubeconfig to use our binary with kubelogin subcommand
func updateKubeconfigCommand(kubeconfigPath string) error {
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

	// Write the updated kubeconfig back
	if err := clientcmd.WriteToFile(*config, kubeconfigPath); err != nil {
		return fmt.Errorf("failed to write updated kubeconfig: %w", err)
	}

	return nil
}
