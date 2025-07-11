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

package base

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/client-go/tools/clientcmd"
)

// RawBaseOptions represents shared configuration options for all breakglass commands.
type RawBaseOptions struct {
	KubeconfigPath string // Path to kubeconfig file for Kubernetes access
}

// DefaultBaseOptions returns a new RawBaseOptions struct initialized with sensible defaults
// for the kubeconfig path.
func DefaultBaseOptions() *RawBaseOptions {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	kubeconfig := loadingRules.GetDefaultFilename()

	return &RawBaseOptions{
		KubeconfigPath: kubeconfig,
	}
}

// BindBaseOptions configures cobra command flags for base options shared across all breakglass commands.
func BindBaseOptions(opts *RawBaseOptions, cmd *cobra.Command) error {
	kubeconfigFlag := "kubeconfig"
	cmd.Flags().StringVar(&opts.KubeconfigPath, kubeconfigFlag, opts.KubeconfigPath, "path to the kubeconfig file")

	if err := cmd.MarkFlagFilename(kubeconfigFlag); err != nil {
		return fmt.Errorf("failed to mark flag %q as a file: %w", kubeconfigFlag, err)
	}
	return nil
}

// ValidateBaseOptions performs validation of base options shared across all breakglass commands.
func ValidateBaseOptions(opts *RawBaseOptions) error {
	if _, err := os.Stat(opts.KubeconfigPath); err != nil {
		return fmt.Errorf("kubeconfig not found at %s: %w", opts.KubeconfigPath, err)
	}

	return nil
}
