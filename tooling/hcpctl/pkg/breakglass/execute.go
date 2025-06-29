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

// Package breakglass provides functionality for creating emergency access to HyperShift-managed clusters.
//
// The breakglass package implements a workflow that allows SREs to generate temporary certificates
// for emergency access to managed clusters. The workflow includes:
//
//  1. Retrieving the cluster's certificate authority (CA) certificate
//  2. Generating a private key and certificate signing request (CSR)
//  3. Submitting the CSR to the cluster and waiting for approval
//  4. Creating a kubeconfig file with the signed certificate
//  5. Optionally setting up port-forwarding to enable kubectl access
//
// Example usage:
//
//	params := &breakglass.ExecutionParams{
//		ClusterID:   "my-cluster",
//		User:        "admin",
//		Namespace:   "clusters-my-cluster",
//		OutputPath:  "kubeconfig",
//		EnablePortForward: true,
//	}
//
//	if err := breakglass.Execute(ctx, params); err != nil {
//		log.Fatal(err)
//	}
//
// The generated kubeconfig can then be used with kubectl to access the cluster:
//
//	export KUBECONFIG=./kubeconfig
//	kubectl get nodes
package breakglass

import (
	"context"
	"crypto/rsa"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/breakglass/certs"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/breakglass/minting"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/breakglass/portforward"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/shell"
)

// ExecutionParams contains all the parameters needed to execute the breakglass workflow.
// This struct is designed to be created by the cmd package and passed to the pkg execution functions,
// maintaining the principle that pkg should not depend on cmd.
type ExecutionParams struct {
	// Cluster and user identification
	ClusterID   string
	ClusterName string
	User        string
	Namespace   string

	// Output configuration
	OutputPath string
	Timeout    time.Duration

	// Behavior flags
	EnablePortForward bool
	EnableShell       bool

	// Kubernetes configuration
	RestConfig *rest.Config

	// Configuration
	Config *Config
}

const (
	// CSR operation timeouts
	CSRPollInterval = 1 * time.Second  // Interval between CSR status checks
	CSRTimeout      = 15 * time.Second // Maximum time to wait for CSR approval and certificate issuance

	// Port forwarding timeouts
	PortForwardTimeout      = 10 * time.Second // Maximum time to wait for port forwarding to start
	PortForwardReadyTimeout = 5 * time.Second  // Maximum time to wait for port forwarding readiness

	// Certificate generation constants
	DefaultRSAKeySize = 2048 // Default RSA key size for certificate generation

	// Network constants
	KubeAPIServerPort = 6443 // Standard Kubernetes API server port
)

// SanitizeUsername converts a username to a format suitable for Kubernetes resource names.
// It follows DNS-1123 label requirements and fails on invalid input rather than trying to fix it.
func SanitizeUsername(username string) (string, error) {
	if username == "" {
		return "", fmt.Errorf("username cannot be empty")
	}

	// Convert to lowercase and replace invalid characters with hyphens
	var result strings.Builder
	for _, r := range strings.ToLower(username) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			result.WriteRune(r)
		} else {
			result.WriteRune('-')
		}
	}

	sanitized := result.String()

	// Remove leading and trailing hyphens
	sanitized = strings.Trim(sanitized, "-")

	// Fail if result is problematic
	if sanitized == "" {
		return "", fmt.Errorf("username %q contains no valid characters", username)
	}

	// Truncate to 63 characters if needed
	if len(sanitized) > 63 {
		sanitized = sanitized[:63]
		// Remove trailing hyphen if truncation created one
		sanitized = strings.TrimSuffix(sanitized, "-")
		if sanitized == "" {
			return "", fmt.Errorf("username %q is too long and contains no valid characters", username)
		}
	}

	return sanitized, nil
}

// Execute runs the breakglass workflow with the provided parameters
func Execute(ctx context.Context, params *ExecutionParams) error {
	logger := logr.FromContextOrDiscard(ctx)
	logger.Info("Starting breakglass workflow", "clusterId", params.ClusterID, "user", params.User)

	// Variables for cleanup tracking
	var csrName string
	var csrApprovalCreated bool
	var portForwardStopCh chan struct{}
	var portForwardStopOnce *sync.Once

	// Setup cleanup functions in reverse order (LIFO)
	defer func() {
		if portForwardStopCh != nil && portForwardStopOnce != nil {
			logger.V(1).Info("Stopping port forwarding")
			portForwardStopOnce.Do(func() {
				close(portForwardStopCh)
			})
		}
	}()
	defer func() {
		if csrApprovalCreated && csrName != "" {
			if err := cleanupCSRApproval(context.Background(), params, csrName); err != nil {
				logger.Error(err, "CSR approval cleanup failed", "csr", csrName)
			}
		}
	}()
	defer func() {
		if csrName != "" {
			if err := cleanupCSR(context.Background(), params, csrName); err != nil {
				logger.Error(err, "CSR cleanup failed", "csr", csrName)
			}
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, params.Timeout)
	defer cancel()

	// Determine local port for API server access
	localPort := KubeAPIServerPort
	if params.EnablePortForward {
		// Find a free port for port forwarding
		freePort, err := portforward.FindFreePort()
		if err != nil {
			return fmt.Errorf("failed to find free port: %w", err)
		}
		localPort = freePort
		logger.V(7).Info("Found free port for forwarding", "port", localPort)
	}

	// Step 1: Get the KAS CA certificate
	caCert, err := getKASCACertificate(ctx, params)
	if err != nil {
		return fmt.Errorf("failed to get KAS CA certificate: %w", err)
	}

	// Step 2: Generate client certificate and private key
	privateKey, err := certs.GeneratePrivateKey(DefaultRSAKeySize)
	if err != nil {
		return NewCertificateError("generation", "private key", err)
	}

	subject := certs.BuildSubject(params.User)
	csrPEM, err := certs.GenerateCSR(privateKey, subject)
	if err != nil {
		return NewCertificateError("generation", "CSR", err)
	}
	logger.V(7).Info("Generated private key and CSR", "keySize", DefaultRSAKeySize, "subject", subject.String(), "user", params.User)

	// Step 3: Submit CSR for approval
	csrName, csrApprovalCreated, err = submitCSR(ctx, params, csrPEM)
	if err != nil {
		return fmt.Errorf("failed to submit CSR: %w", err)
	}
	logger.V(7).Info("Submitted CSR", "name", csrName)

	// Step 4: Wait for CSR approval and certificate
	clientCert, err := waitForCSRApproval(ctx, params, csrName)
	if err != nil {
		return fmt.Errorf("failed to get approved certificate: %w", err)
	}
	logger.V(7).Info("CSR approved and certificate retrieved", "csr", csrName, "user", params.User, "cluster", params.ClusterID, "certBytes", len(clientCert))

	// Step 5: Create kubeconfig
	if err := createKubeconfig(ctx, params, privateKey, clientCert, caCert, localPort); err != nil {
		return fmt.Errorf("failed to create kubeconfig: %w", err)
	}

	// Step 6: Setup port forwarding if enabled
	if params.EnablePortForward {
		portForwardStopCh, portForwardStopOnce, err = setupPortForwarding(ctx, params, localPort)
		if err != nil {
			return fmt.Errorf("failed to setup port forwarding: %w", err)
		}
	}

	return nil
}

// getKASCACertificate retrieves the cluster's certificate authority certificate
func getKASCACertificate(ctx context.Context, params *ExecutionParams) ([]byte, error) {
	logger := logr.FromContextOrDiscard(ctx)

	// Create kubernetes client from rest config
	kubeClient, err := kubernetes.NewForConfig(params.RestConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Get the KAS CA certificate from the kas-server-crt secret
	secret, err := kubeClient.CoreV1().Secrets(params.Namespace).Get(ctx, "kas-server-crt", metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get kas-server-crt secret: %w", err)
	}

	caCertData, exists := secret.Data["tls.crt"]
	if !exists {
		return nil, NewCertificateError("retrieval", "CA certificate", fmt.Errorf("tls.crt not found in kas-server-crt secret"))
	}

	logger.V(7).Info("Retrieved KAS CA certificate from secret", "secret", "kas-server-crt", "namespace", params.Namespace, "bytes", len(caCertData))
	return caCertData, nil
}

// submitCSR submits a certificate signing request to the cluster
func submitCSR(ctx context.Context, params *ExecutionParams, csrPEM []byte) (string, bool, error) {
	logger := logr.FromContextOrDiscard(ctx)

	sanitizedUser, err := SanitizeUsername(params.User)
	if err != nil {
		return "", false, fmt.Errorf("invalid username for CSR naming: %w", err)
	}

	csrManager, err := minting.NewDefaultManager(params.RestConfig)
	if err != nil {
		return "", false, fmt.Errorf("failed to create CSR manager: %w", err)
	}

	// Create CSR using the parameter-based method
	csrName, err := csrManager.CreateCSR(ctx, csrPEM, params.ClusterID, sanitizedUser, params.Namespace)
	if err != nil {
		return "", false, fmt.Errorf("failed to create CSR: %w", err)
	}

	logger.V(7).Info("CSR created", "name", csrName, "user", params.User, "cluster", params.ClusterID)

	// Create CSR approval using the parameter-based method
	err = csrManager.CreateCSRApproval(ctx, csrName, params.Namespace, params.ClusterID, sanitizedUser)
	if err != nil {
		return csrName, false, fmt.Errorf("failed to create CSR approval: %w", err)
	}

	logger.V(7).Info("CSR approval created", "name", csrName, "namespace", params.Namespace, "cluster", params.ClusterID)

	return csrName, true, nil
}

// waitForCSRApproval waits for the CSR to be approved and returns the certificate
func waitForCSRApproval(ctx context.Context, params *ExecutionParams, csrName string) ([]byte, error) {
	logger := logr.FromContextOrDiscard(ctx)

	csrManager, err := minting.NewDefaultManager(params.RestConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create CSR manager: %w", err)
	}

	// Wait for CSR approval using CSRManager
	err = csrManager.WaitForCSRApproval(ctx, csrName, CSRTimeout, CSRPollInterval)
	if err != nil {
		// Check if this is a timeout error and wrap it appropriately
		if ctx.Err() == context.DeadlineExceeded {
			return nil, NewTimeoutError("CSR approval", CSRTimeout, "CSR to be approved", err)
		}
		return nil, fmt.Errorf("failed to wait for CSR approval: %w", err)
	}

	logger.V(7).Info("CSR is approved", "name", csrName)

	// Wait for certificate to be issued using CSRManager
	certificate, err := csrManager.WaitForCertificate(ctx, csrName, CSRTimeout, CSRPollInterval)
	if err != nil {
		// Check if this is a timeout error and wrap it appropriately
		if ctx.Err() == context.DeadlineExceeded {
			return nil, NewTimeoutError("certificate issuance", CSRTimeout, "certificate to be issued", err)
		}
		return nil, fmt.Errorf("failed to wait for certificate: %w", err)
	}

	return certificate, nil
}

// cleanupCSR removes only the CSR resource
func cleanupCSR(ctx context.Context, params *ExecutionParams, csrName string) error {
	logger := logr.FromContextOrDiscard(ctx)

	csrManager, err := minting.NewDefaultManager(params.RestConfig)
	if err != nil {
		logger.V(1).Info("Failed to create CSR manager for cleanup", "error", err)
		return nil // Don't fail cleanup for this
	}

	// Delete only the CSR (ignore errors since it might not exist)
	if err := csrManager.CleanupCSR(ctx, csrName); err != nil {
		logger.V(1).Info("CSR cleanup failed", "name", csrName, "error", err)
	} else {
		logger.V(7).Info("CSR cleanup completed", "name", csrName)
	}

	return nil // Always return nil for cleanup functions to not fail the entire cleanup process
}

// cleanupCSRApproval removes only the CSR approval resource
func cleanupCSRApproval(ctx context.Context, params *ExecutionParams, csrName string) error {
	logger := logr.FromContextOrDiscard(ctx)

	csrManager, err := minting.NewDefaultManager(params.RestConfig)
	if err != nil {
		logger.V(1).Info("Failed to create CSR manager for cleanup", "error", err)
		return nil // Don't fail cleanup for this
	}

	// Delete only the CSR approval (ignore errors since it might not exist)
	if err := csrManager.CleanupCSRApproval(ctx, csrName, params.Namespace); err != nil {
		logger.V(1).Info("CSR approval cleanup failed", "name", csrName, "error", err)
	} else {
		logger.V(7).Info("CSR approval cleanup completed", "name", csrName)
	}

	return nil // Always return nil for cleanup functions to not fail the entire cleanup process
}

// createKubeconfig creates a kubeconfig file with the generated certificate
func createKubeconfig(ctx context.Context, params *ExecutionParams, privateKey *rsa.PrivateKey, clientCert, caCert []byte, localPort int) error {
	logger := logr.FromContextOrDiscard(ctx)

	// Convert private key to PEM format
	privateKeyPEM := certs.EncodePrivateKey(privateKey)

	// Sanitize username for kubeconfig authInfo name
	sanitizedUser, err := SanitizeUsername(params.User)
	if err != nil {
		return fmt.Errorf("invalid username for kubeconfig authInfo: %w", err)
	}

	// Create kubeconfig
	config := &clientcmdapi.Config{
		APIVersion: "v1",
		Kind:       "Config",
		Clusters: map[string]*clientcmdapi.Cluster{
			params.ClusterID: {
				Server:                   fmt.Sprintf("https://127.0.0.1:%d", localPort),
				CertificateAuthorityData: caCert,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			sanitizedUser: {
				ClientCertificateData: clientCert,
				ClientKeyData:         privateKeyPEM,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			params.ClusterID: {
				Cluster:  params.ClusterID,
				AuthInfo: sanitizedUser,
			},
		},
		CurrentContext: params.ClusterID,
	}

	// Write kubeconfig to file
	if err := clientcmd.WriteToFile(*config, params.OutputPath); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	logger.V(7).Info("Kubeconfig written", "path", params.OutputPath)
	return nil
}

// setupPortForwarding sets up port forwarding to the cluster's API server
func setupPortForwarding(ctx context.Context, params *ExecutionParams, localPort int) (chan struct{}, *sync.Once, error) {
	logger := logr.FromContextOrDiscard(ctx)

	// Use the standard KAS service name with service/ prefix for proper resolution
	kasServiceTarget := "service/kube-apiserver"

	// Setup port forwarding
	forwarder, err := portforward.New(params.RestConfig, params.Namespace, kasServiceTarget, localPort, KubeAPIServerPort)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create port forwarder: %w", err)
	}

	// Start port forwarding in a goroutine
	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	stopOnce := &sync.Once{} // Prevent double-close

	go func() {
		if err := forwarder.ForwardPorts(ctx, stopCh, readyCh); err != nil {
			logger.Error(err, "Port forwarding failed")
		}
	}()

	// Wait for port forwarding to be ready
	select {
	case <-readyCh:
		logger.Info("Port forwarding established", "service", kasServiceTarget, "localPort", localPort, "remotePort", KubeAPIServerPort, "namespace", params.Namespace)
	case <-ctx.Done():
		stopOnce.Do(func() { close(stopCh) })
		return nil, nil, fmt.Errorf("context cancelled while waiting for port forwarding")
	case <-time.After(PortForwardTimeout):
		stopOnce.Do(func() { close(stopCh) })
		return nil, nil, NewTimeoutError("port forwarding setup", PortForwardTimeout, "port forwarding to be ready", nil)
	}

	// Handle different modes based on shell option
	if params.EnableShell {
		logger.Info("Starting shell with kubeconfig", "kubeconfig", params.OutputPath)
		err := shell.SpawnWithCleanup(ctx, &shell.Config{
			KubeconfigPath: params.OutputPath,
			ClusterName:    params.ClusterName,
			ClusterID:      params.ClusterID,
			PromptInfo:     fmt.Sprintf("[%s:%s]", params.ClusterID, params.ClusterName),
		}, stopCh, stopOnce)
		return stopCh, stopOnce, err
	} else {
		err := runWaitMode(ctx, stopCh, stopOnce, logger)
		return stopCh, stopOnce, err
	}
}

// runWaitMode implements the original behavior - wait for SIGINT/SIGTERM
func runWaitMode(ctx context.Context, stopCh chan struct{}, stopOnce *sync.Once, logger logr.Logger) error {
	// Keep port forwarding alive until interrupted
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	logger.Info("Port forwarding active. Press Ctrl+C to terminate.")

	// Wait for interrupt signal or context cancellation
	select {
	case sig := <-sigCh:
		logger.Info("Received signal, stopping port forwarding", "signal", sig)
		stopOnce.Do(func() { close(stopCh) })
		return nil
	case <-ctx.Done():
		logger.Info("Context cancelled, stopping port forwarding")
		stopOnce.Do(func() { close(stopCh) })
		return ctx.Err()
	}
}
