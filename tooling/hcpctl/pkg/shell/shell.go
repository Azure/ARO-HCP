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

// Package shell provides unified shell spawning functionality for breakglass operations.
//
// This package consolidates shell handling logic that was previously duplicated
// between MC and HCP breakglass implementations.
package shell

import (
	"context"
	"sync"
)

// Config represents the configuration for spawning a shell with kubeconfig access.
type Config struct {
	// KubeconfigPath is the path to the kubeconfig file to set as KUBECONFIG
	KubeconfigPath string

	// ClusterName is the display name of the cluster for prompts
	ClusterName string

	// ClusterID is the unique identifier of the cluster (used in HCP scenarios)
	ClusterID string

	// PromptInfo is the formatted prompt information to display in the shell
	// Examples: "[MC: cluster-name]" or "[cluster-id:cluster-name]"
	PromptInfo string
}

// Spawn spawns an interactive shell with KUBECONFIG environment set.
// This is a convenience function for simple use cases that handles cleanup
// coordination internally.
//
// The shell will run with the specified kubeconfig and custom prompt.
// This function blocks until the shell exits or the context is cancelled.
func Spawn(ctx context.Context, config *Config) error {
	stopCh := make(chan struct{})
	stopOnce := &sync.Once{}
	return SpawnWithCleanup(ctx, config, stopCh, stopOnce)
}

// SpawnWithCleanup spawns an interactive shell with advanced cleanup coordination.
// This function provides full control over the shell lifecycle and coordination
// with other background operations (like port forwarding).
//
// The stopCh channel is used to signal when cleanup should begin, and stopOnce
// ensures cleanup operations only happen once. This is essential for scenarios
// where multiple goroutines need to coordinate shutdown.
//
// This function blocks until the shell exits or the context is cancelled.
func SpawnWithCleanup(ctx context.Context, config *Config, stopCh chan struct{}, stopOnce *sync.Once) error {
	return spawnShell(ctx, config, stopCh, stopOnce)
}
