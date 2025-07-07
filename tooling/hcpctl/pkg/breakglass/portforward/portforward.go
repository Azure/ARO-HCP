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

package portforward

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// Forwarder is an interface for port forwarding operations.
// This interface abstracts port forwarding setup and management to enable
// testing and potential alternative port forwarding implementations.
type Forwarder interface {
	// FindFreePort finds an available local port for port forwarding
	FindFreePort() (int, error)

	// ForwardPorts establishes port forwarding from local to remote port
	// stopCh is used to signal when to stop forwarding
	// readyCh is signaled when forwarding is established
	ForwardPorts(ctx context.Context, stopCh <-chan struct{}, readyCh chan struct{}) error
}

// FindFreePort finds a free local port
func FindFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to find free port: %w", err)
	}
	defer listener.Close()

	addr := listener.Addr().(*net.TCPAddr)
	return addr.Port, nil
}

// PortForwarder handles port forwarding to a Kubernetes service
type PortForwarder struct {
	restConfig    *rest.Config
	kubeClient    kubernetes.Interface
	namespace     string
	targetService string
	localPort     int
	remotePort    int
}

// New creates a new PortForwarder
func New(restConfig *rest.Config, namespace, target string, localPort, remotePort int) (*PortForwarder, error) {
	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return &PortForwarder{
		restConfig:    restConfig,
		kubeClient:    kubeClient,
		namespace:     namespace,
		targetService: target,
		localPort:     localPort,
		remotePort:    remotePort,
	}, nil
}

// FindFreePort finds an available local port for port forwarding
func (pf *PortForwarder) FindFreePort() (int, error) {
	return FindFreePort()
}

// ForwardPorts starts port forwarding with graceful error handling
func (pf *PortForwarder) ForwardPorts(ctx context.Context, stopCh <-chan struct{}, readyCh chan struct{}) error {
	logger := logr.FromContextOrDiscard(ctx)

	// Resolve service to a pod
	podName, err := pf.resolveServiceToPod(ctx, pf.targetService)
	if err != nil {
		return fmt.Errorf("failed to resolve service %s to pod: %w", pf.targetService, err)
	}
	logger.V(1).Info("resolved service to pod", "service", pf.targetService, "pod", podName)

	// Build the URL for port forwarding to the resolved pod
	pfURL, err := url.Parse(fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s/portforward",
		pf.restConfig.Host,
		pf.namespace,
		podName,
	))
	if err != nil {
		return fmt.Errorf("failed to build port forward URL: %w", err)
	}

	// Create the SPDY transport
	transport, upgrader, err := spdy.RoundTripperFor(pf.restConfig)
	if err != nil {
		return fmt.Errorf("failed to create round tripper: %w", err)
	}

	// Create the port forwarding dialer
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", pfURL)

	// Set up port forwarding
	ports := []string{fmt.Sprintf("%d:%d", pf.localPort, pf.remotePort)}

	ioStreams := genericclioptions.IOStreams{
		In:     nil,
		Out:    io.Discard, // Suppress output
		ErrOut: io.Discard, // Suppress error output to eliminate noisy connection cleanup messages
	}

	logger.V(1).Info("creating port forwarder",
		"service", pf.targetService,
		"namespace", pf.namespace,
		"ports", ports,
	)

	fw, err := portforward.New(dialer, ports, stopCh, readyCh, ioStreams.Out, ioStreams.ErrOut)
	if err != nil {
		return fmt.Errorf("failed to create port forwarder: %w", err)
	}

	// Start port forwarding
	return fw.ForwardPorts()
}

// resolveServiceToPod resolves a service to a specific pod, mimicking kubectl's behavior
func (pf *PortForwarder) resolveServiceToPod(ctx context.Context, serviceName string) (string, error) {
	// Get the service to find its selector
	service, err := pf.kubeClient.CoreV1().Services(pf.namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get service %s: %w", serviceName, err)
	}

	if len(service.Spec.Selector) == 0 {
		return "", fmt.Errorf("service %s has no selector", serviceName)
	}

	// Convert selector map to label selector string
	labelSelector := metav1.FormatLabelSelector(&metav1.LabelSelector{
		MatchLabels: service.Spec.Selector,
	})

	// Get pods matching the service selector
	pods, err := pf.kubeClient.CoreV1().Pods(pf.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list pods for service %s: %w", serviceName, err)
	}

	// Find the first running and ready pod (mimicking kubectl's behavior)
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			// Check if pod is ready
			allReady := true
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodReady && condition.Status != corev1.ConditionTrue {
					allReady = false
					break
				}
			}
			if allReady {
				return pod.Name, nil
			}
		}
	}

	return "", fmt.Errorf("no running and ready pods found for service %s", serviceName)
}
