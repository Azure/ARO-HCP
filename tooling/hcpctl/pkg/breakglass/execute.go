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

package breakglass

import (
	"context"
	"crypto/rsa"
	"fmt"
	"os"
	"os/signal"
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
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/utils"
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
	Privileged  bool

	// Output configuration
	OutputPath string
	Timeout    time.Duration

	// Behavior flags
	EnablePortForward bool
	EnableShell       bool

	// Command execution
	ExecCommand string

	// Kubernetes configuration
	RestConfig *rest.Config
}

const (
	// CSR operation timeouts
	CSRTimeout = 15 * time.Second // Maximum time to wait for CSR approval and certificate issuance

	// Port forwarding timeouts
	PortForwardTimeout      = 30 * time.Second // Maximum time to wait for port forwarding to start
	PortForwardReadyTimeout = 5 * time.Second  // Maximum time to wait for port forwarding readiness

	// Certificate generation constants
	DefaultRSAKeySize = 2048 // Default RSA key size for certificate generation

	// Network constants
	KubeAPIServerPort = 6443 // Standard Kubernetes API server port
)

// Execute runs the breakglass workflow with the provided parameters
func Execute(ctx context.Context, params *ExecutionParams) error {
	logger := logr.FromContextOrDiscard(ctx)
	logger.V(1).Info("Starting breakglass workflow", "clusterId", params.ClusterID, "user", params.User, "privileged", params.Privileged)

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
	localPort, err := determineLocalPort(ctx, params)
	if err != nil {
		return err
	}

	// Step 1: Generate certificates and get cluster CA
	privateKey, clientCert, caCert, err := generateCertificates(ctx, params, &csrName, &csrApprovalCreated)
	if err != nil {
		return err
	}

	// Step 2: Create kubeconfig
	if err := createKubeconfig(ctx, params, privateKey, clientCert, caCert, localPort); err != nil {
		return err
	}

	// Step 3: Setup port forwarding if enabled
	if params.EnablePortForward {
		portForwardStopCh, portForwardStopOnce, err = setupPortForwarding(ctx, params, localPort)
		if err != nil {
			return err
		}

		// Step 4: Handle exec, shell, or wait mode
		if params.ExecCommand != "" {
			if err := executeCommand(ctx, params, portForwardStopCh, portForwardStopOnce); err != nil {
				return err
			}
		} else if params.EnableShell {
			if err := startBreakglassShell(ctx, params, portForwardStopCh, portForwardStopOnce); err != nil {
				return err
			}
		} else {
			if err := runWaitMode(ctx, portForwardStopCh, portForwardStopOnce); err != nil {
				return err
			}
		}
	}

	return nil
}

// determineLocalPort determines the local port to use for API server access
func determineLocalPort(ctx context.Context, params *ExecutionParams) (int, error) {
	logger := logr.FromContextOrDiscard(ctx)
	localPort := KubeAPIServerPort

	if params.EnablePortForward {
		freePort, err := portforward.FindFreePort()
		if err != nil {
			return 0, fmt.Errorf("failed to find free port: %w", err)
		}
		localPort = freePort
		logger.V(7).Info("Found free port for forwarding", "port", localPort)
	}

	return localPort, nil
}

// generateCertificates handles the complete certificate generation workflow
func generateCertificates(ctx context.Context, params *ExecutionParams, csrName *string, csrApprovalCreated *bool) (*rsa.PrivateKey, []byte, []byte, error) {
	logger := logr.FromContextOrDiscard(ctx)

	// Step 1: Get the KAS CA certificate
	caCert, err := getKASCACertificate(ctx, params)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get KAS CA certificate: %w", err)
	}

	// Step 2: Generate client certificate and private key
	privateKey, err := certs.GeneratePrivateKey(DefaultRSAKeySize)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	subject := certs.BuildSubject(params.User, params.Privileged)
	csrPEM, err := certs.GenerateCSR(privateKey, subject)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate CSR: %w", err)
	}
	logger.V(7).Info("Generated private key and CSR", "keySize", DefaultRSAKeySize, "subject", subject.String(), "user", params.User)

	// Step 3: Submit CSR for approval
	var submitErr error
	*csrName, *csrApprovalCreated, submitErr = submitCSR(ctx, params, csrPEM)
	if submitErr != nil {
		return nil, nil, nil, fmt.Errorf("failed to submit CSR: %w", submitErr)
	}
	logger.V(7).Info("Submitted CSR", "name", *csrName)

	// Step 4: Wait for CSR approval and certificate
	clientCert, err := waitForCSRApproval(ctx, params, *csrName)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get approved certificate: %w", err)
	}
	logger.V(7).Info("CSR approved and certificate retrieved", "csr", *csrName, "user", params.User, "cluster", params.ClusterID, "certBytes", len(clientCert))

	return privateKey, clientCert, caCert, nil
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
		return nil, fmt.Errorf("tls.crt not found in kas-server-crt secret")
	}

	logger.V(7).Info("Retrieved KAS CA certificate from secret", "secret", "kas-server-crt", "namespace", params.Namespace, "bytes", len(caCertData))
	return caCertData, nil
}

// submitCSR submits a certificate signing request to the cluster
func submitCSR(ctx context.Context, params *ExecutionParams, csrPEM []byte) (string, bool, error) {
	logger := logr.FromContextOrDiscard(ctx)

	csrManager, err := minting.NewDefaultManager(params.RestConfig)
	if err != nil {
		return "", false, fmt.Errorf("failed to create CSR manager: %w", err)
	}

	// Create CSR using the parameter-based method
	csrName, err := csrManager.CreateCSR(ctx, csrPEM, params.ClusterID, params.User, params.Namespace)
	if err != nil {
		return "", false, fmt.Errorf("failed to create CSR: %w", err)
	}

	logger.V(7).Info("CSR created", "name", csrName, "user", params.User, "cluster", params.ClusterID)

	// Create CSR approval using the parameter-based method
	err = csrManager.CreateCSRApproval(ctx, csrName, params.Namespace, params.ClusterID, params.User)
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

	// Wait for CSR approval using CSRManager (now with watch)
	err = csrManager.WaitForCSRApproval(ctx, csrName, CSRTimeout)
	if err != nil {
		return nil, err
	}

	logger.V(7).Info("CSR is approved", "name", csrName)

	// Wait for certificate to be issued using CSRManager (now with watch)
	certificate, err := csrManager.WaitForCertificate(ctx, csrName, CSRTimeout)
	if err != nil {
		return nil, err
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

	return nil
}

// createKubeconfig creates a kubeconfig file with the generated certificate
func createKubeconfig(ctx context.Context, params *ExecutionParams, privateKey *rsa.PrivateKey, clientCert, caCert []byte, localPort int) error {
	logger := logr.FromContextOrDiscard(ctx)

	// Convert private key to PEM format
	privateKeyPEM := certs.EncodePrivateKey(privateKey)

	// Sanitize username for kubeconfig authInfo name
	sanitizedUser, err := utils.SanitizeUsername(params.User)
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

	// Use the standard KAS service name
	kasServiceName := "kube-apiserver"

	// Start port forwarding in a goroutine
	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	stopOnce := &sync.Once{} // Prevent double-close

	go func() {
		if err := portforward.ForwardToService(ctx, params.RestConfig, params.Namespace, kasServiceName, localPort, KubeAPIServerPort, stopCh, readyCh); err != nil {
			logger.Error(err, "Port forwarding failed")
		}
	}()

	// Wait for port forwarding to be ready
	select {
	case <-readyCh:
		logger.V(1).Info("Port forwarding established", "service", kasServiceName, "localPort", localPort, "remotePort", KubeAPIServerPort, "namespace", params.Namespace)
	case <-ctx.Done():
		stopOnce.Do(func() { close(stopCh) })
		return nil, nil, fmt.Errorf("context cancelled while waiting for port forwarding")
	case <-time.After(PortForwardTimeout):
		stopOnce.Do(func() { close(stopCh) })
		return nil, nil, fmt.Errorf("timeout waiting for port forwarding setup")
	}

	return stopCh, stopOnce, nil
}

// startBreakglassShell starts an interactive shell with the provided kubeconfig
func startBreakglassShell(ctx context.Context, params *ExecutionParams, stopCh chan struct{}, stopOnce *sync.Once) error {
	logger := logr.FromContextOrDiscard(ctx)
	logger.V(1).Info("Starting shell with kubeconfig", "kubeconfig", params.OutputPath)
	err := shell.SpawnWithCleanup(ctx, &shell.Config{
		KubeconfigPath: params.OutputPath,
		ClusterName:    params.ClusterName,
		ClusterID:      params.ClusterID,
		PromptInfo:     fmt.Sprintf("[HCP:%s:%s]", params.ClusterID, params.ClusterName),
		Privileged:     params.Privileged,
	}, stopCh, stopOnce)
	// Shell exit is expected - don't treat it as an error
	if err != nil {
		logger.V(1).Info("Shell exited", "error", err)
	}
	return nil
}

// runWaitMode implements the original behavior - wait for SIGINT/SIGTERM
func runWaitMode(ctx context.Context, stopCh chan struct{}, stopOnce *sync.Once) error {
	logger := logr.FromContextOrDiscard(ctx)
	// Keep port forwarding alive until interrupted
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	logger.V(1).Info("Port forwarding active. Press Ctrl+C to terminate.")

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

// executeCommand executes a direct command with the kubeconfig environment set
func executeCommand(ctx context.Context, params *ExecutionParams, stopCh chan struct{}, stopOnce *sync.Once) error {
	logger := logr.FromContextOrDiscard(ctx)
	logger.V(1).Info("Executing command with kubeconfig", "kubeconfig", params.OutputPath, "command", params.ExecCommand)

	err := shell.ExecCommandString(ctx, &shell.Config{
		KubeconfigPath: params.OutputPath,
		ClusterName:    params.ClusterName,
		ClusterID:      params.ClusterID,
		PromptInfo:     fmt.Sprintf("[HCP:%s:%s]", params.ClusterID, params.ClusterName),
		Privileged:     params.Privileged,
	}, params.ExecCommand, stopCh, stopOnce)

	// Return the actual exit code/error from the command execution
	// This allows the breakglass process to exit with the same code as the executed command
	if err != nil {
		logger.V(1).Info("Command exited", "error", err)
	}
	return err
}
