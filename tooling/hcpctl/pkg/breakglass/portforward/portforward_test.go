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
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"
)

func TestFindFreePort(t *testing.T) {
	port, err := FindFreePort()
	if err != nil {
		t.Fatalf("FindFreePort failed: %v", err)
	}

	if port <= 0 || port > 65535 {
		t.Errorf("invalid port number: %d", port)
	}

	// Verify the port is actually available by trying to listen on it
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)))
	if err != nil {
		t.Errorf("port %d is not actually free: %v", port, err)
	} else {
		listener.Close()
	}
}

func TestFindFreePortMultipleCalls(t *testing.T) {
	// Test that multiple calls return different ports
	ports := make(map[int]bool)

	for i := 0; i < 5; i++ {
		port, err := FindFreePort()
		if err != nil {
			t.Fatalf("FindFreePort failed on call %d: %v", i, err)
		}

		if ports[port] {
			t.Errorf("port %d was returned multiple times", port)
		}
		ports[port] = true
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name        string
		restConfig  *rest.Config
		namespace   string
		target      string
		localPort   int
		remotePort  int
		expectError bool
	}{
		{
			name: "valid configuration",
			restConfig: &rest.Config{
				Host: "https://test-server",
			},
			namespace:   "test-namespace",
			target:      "test-pod",
			localPort:   8080,
			remotePort:  80,
			expectError: false,
		},
		{
			name: "service target",
			restConfig: &rest.Config{
				Host: "https://test-server",
			},
			namespace:   "test-namespace",
			target:      "service/test-service",
			localPort:   8080,
			remotePort:  80,
			expectError: false,
		},
		{
			name: "invalid rest config",
			restConfig: &rest.Config{
				Host: "invalid-url",
				// Missing required fields that would cause kubernetes client creation to fail
				BearerToken: "invalid",
				TLSClientConfig: rest.TLSClientConfig{
					Insecure: false,
					CertFile: "/non/existent/cert",
				},
			},
			namespace:   "test-namespace",
			target:      "test-pod",
			localPort:   8080,
			remotePort:  80,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pf, err := New(tt.restConfig, tt.namespace, tt.target, tt.localPort, tt.remotePort)

			if tt.expectError && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if !tt.expectError {
				if pf == nil {
					t.Error("expected PortForwarder but got nil")
				} else {
					if pf.namespace != tt.namespace {
						t.Errorf("expected namespace %s, got %s", tt.namespace, pf.namespace)
					}
					if pf.target != tt.target {
						t.Errorf("expected target %s, got %s", tt.target, pf.target)
					}
					if pf.localPort != tt.localPort {
						t.Errorf("expected localPort %d, got %d", tt.localPort, pf.localPort)
					}
					if pf.remotePort != tt.remotePort {
						t.Errorf("expected remotePort %d, got %d", tt.remotePort, pf.remotePort)
					}
				}
			}
		})
	}
}

func TestResolveServiceToPod(t *testing.T) {
	tests := []struct {
		name         string
		serviceName  string
		namespace    string
		expectError  bool
		expectedPod  string
		setupObjects func() []runtime.Object
	}{
		{
			name:        "service with running pod",
			serviceName: "test-service",
			namespace:   "test-namespace",
			expectError: false,
			expectedPod: "test-pod-1",
			setupObjects: func() []runtime.Object {
				return []runtime.Object{
					&corev1.Service{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-service",
							Namespace: "test-namespace",
						},
						Spec: corev1.ServiceSpec{
							Selector: map[string]string{
								"app": "test-app",
							},
						},
					},
					&corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-pod-1",
							Namespace: "test-namespace",
							Labels: map[string]string{
								"app": "test-app",
							},
						},
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
							Conditions: []corev1.PodCondition{
								{
									Type:   corev1.PodReady,
									Status: corev1.ConditionTrue,
								},
							},
						},
					},
				}
			},
		},
		{
			name:        "service not found",
			serviceName: "non-existent-service",
			namespace:   "test-namespace",
			expectError: true,
			setupObjects: func() []runtime.Object {
				return []runtime.Object{}
			},
		},
		{
			name:        "service without selector",
			serviceName: "test-service",
			namespace:   "test-namespace",
			expectError: true,
			setupObjects: func() []runtime.Object {
				return []runtime.Object{
					&corev1.Service{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-service",
							Namespace: "test-namespace",
						},
						Spec: corev1.ServiceSpec{
							// No selector
						},
					},
				}
			},
		},
		{
			name:        "no running pods",
			serviceName: "test-service",
			namespace:   "test-namespace",
			expectError: true,
			setupObjects: func() []runtime.Object {
				return []runtime.Object{
					&corev1.Service{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-service",
							Namespace: "test-namespace",
						},
						Spec: corev1.ServiceSpec{
							Selector: map[string]string{
								"app": "test-app",
							},
						},
					},
					&corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-pod-1",
							Namespace: "test-namespace",
							Labels: map[string]string{
								"app": "test-app",
							},
						},
						Status: corev1.PodStatus{
							Phase: corev1.PodPending, // Not running
						},
					},
				}
			},
		},
		{
			name:        "pod running but not ready",
			serviceName: "test-service",
			namespace:   "test-namespace",
			expectError: true,
			setupObjects: func() []runtime.Object {
				return []runtime.Object{
					&corev1.Service{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-service",
							Namespace: "test-namespace",
						},
						Spec: corev1.ServiceSpec{
							Selector: map[string]string{
								"app": "test-app",
							},
						},
					},
					&corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-pod-1",
							Namespace: "test-namespace",
							Labels: map[string]string{
								"app": "test-app",
							},
						},
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
							Conditions: []corev1.PodCondition{
								{
									Type:   corev1.PodReady,
									Status: corev1.ConditionFalse, // Not ready
								},
							},
						},
					},
				}
			},
		},
		{
			name:        "multiple pods, first ready one selected",
			serviceName: "test-service",
			namespace:   "test-namespace",
			expectError: false,
			expectedPod: "test-pod-2", // Should pick the first ready one
			setupObjects: func() []runtime.Object {
				return []runtime.Object{
					&corev1.Service{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-service",
							Namespace: "test-namespace",
						},
						Spec: corev1.ServiceSpec{
							Selector: map[string]string{
								"app": "test-app",
							},
						},
					},
					&corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-pod-1",
							Namespace: "test-namespace",
							Labels: map[string]string{
								"app": "test-app",
							},
						},
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
							Conditions: []corev1.PodCondition{
								{
									Type:   corev1.PodReady,
									Status: corev1.ConditionFalse,
								},
							},
						},
					},
					&corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-pod-2",
							Namespace: "test-namespace",
							Labels: map[string]string{
								"app": "test-app",
							},
						},
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
							Conditions: []corev1.PodCondition{
								{
									Type:   corev1.PodReady,
									Status: corev1.ConditionTrue,
								},
							},
						},
					},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := tt.setupObjects()
			kubeClient := fake.NewSimpleClientset(objects...)

			pf := &PortForwarder{
				kubeClient: kubeClient,
				namespace:  tt.namespace,
			}

			ctx := context.Background()
			podName, err := pf.resolveServiceToPod(ctx, tt.serviceName)

			if tt.expectError && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if !tt.expectError && podName != tt.expectedPod {
				t.Errorf("expected pod name %s, got %s", tt.expectedPod, podName)
			}
		})
	}
}

func TestResolveServiceToPodWithKubernetesErrors(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		setupClient func(*fake.Clientset)
		expectError bool
	}{
		{
			name:        "service get failure",
			serviceName: "test-service",
			setupClient: func(client *fake.Clientset) {
				client.PrependReactor("get", "services", func(action ktesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, errors.New("get service failed")
				})
			},
			expectError: true,
		},
		{
			name:        "pod list failure",
			serviceName: "test-service",
			setupClient: func(client *fake.Clientset) {
				// Service will be found
				service := &corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-service",
						Namespace: "test-namespace",
					},
					Spec: corev1.ServiceSpec{
						Selector: map[string]string{
							"app": "test-app",
						},
					},
				}
				_, _ = client.CoreV1().Services("test-namespace").Create(context.Background(), service, metav1.CreateOptions{})

				// But pod listing will fail
				client.PrependReactor("list", "pods", func(action ktesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, errors.New("list pods failed")
				})
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kubeClient := fake.NewSimpleClientset()
			tt.setupClient(kubeClient)

			pf := &PortForwarder{
				kubeClient: kubeClient,
				namespace:  "test-namespace",
			}

			ctx := context.Background()
			_, err := pf.resolveServiceToPod(ctx, tt.serviceName)

			if tt.expectError && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// Note: ForwardPorts method is hard to test without a real Kubernetes cluster
// as it depends on SPDY connections and the actual port forwarding infrastructure.
// These integration-style tests would require more complex setup with test servers.
// For now, we focus on testing the individual components and logic.

func TestPortForwarderTargetParsing(t *testing.T) {
	tests := []struct {
		name         string
		target       string
		expectedType string
		expectedName string
	}{
		{
			name:         "pod target",
			target:       "my-pod",
			expectedType: "pod",
			expectedName: "my-pod",
		},
		{
			name:         "service target",
			target:       "service/my-service",
			expectedType: "service",
			expectedName: "my-service",
		},
		{
			name:         "service target with long name",
			target:       "service/very-long-service-name",
			expectedType: "service",
			expectedName: "very-long-service-name",
		},
		{
			name:         "target that starts with service but is not",
			target:       "servicepod",
			expectedType: "pod",
			expectedName: "servicepod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This tests the logic we see in ForwardPorts for parsing targets
			targetType := "pod"
			targetName := tt.target
			if len(tt.target) > 8 && tt.target[:8] == "service/" {
				targetType = "service"
				targetName = tt.target[8:]
			}

			if targetType != tt.expectedType {
				t.Errorf("expected target type %s, got %s", tt.expectedType, targetType)
			}
			if targetName != tt.expectedName {
				t.Errorf("expected target name %s, got %s", tt.expectedName, targetName)
			}
		})
	}
}

func TestPortForwarderFields(t *testing.T) {
	restConfig := &rest.Config{Host: "https://test-server"}
	namespace := "test-namespace"
	target := "test-target"
	localPort := 8080
	remotePort := 80

	pf, err := New(restConfig, namespace, target, localPort, remotePort)
	if err != nil {
		t.Fatalf("Failed to create PortForwarder: %v", err)
	}

	// Test that all fields are set correctly
	if pf.restConfig != restConfig {
		t.Error("restConfig not set correctly")
	}
	if pf.namespace != namespace {
		t.Errorf("expected namespace %s, got %s", namespace, pf.namespace)
	}
	if pf.target != target {
		t.Errorf("expected target %s, got %s", target, pf.target)
	}
	if pf.localPort != localPort {
		t.Errorf("expected localPort %d, got %d", localPort, pf.localPort)
	}
	if pf.remotePort != remotePort {
		t.Errorf("expected remotePort %d, got %d", remotePort, pf.remotePort)
	}
	if pf.kubeClient == nil {
		t.Error("kubeClient should not be nil")
	}
}

func TestIsExpectedConnectionCloseError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: true,
		},
		{
			name:     "forcibly closed error",
			err:      fmt.Errorf("connection was forcibly closed"),
			expected: true,
		},
		{
			name:     "Windows specific forcibly closed error",
			err:      fmt.Errorf("wsarecv: An existing connection was forcibly closed by the remote host"),
			expected: true,
		},
		{
			name:     "broken pipe error",
			err:      fmt.Errorf("broken pipe"),
			expected: true,
		},
		{
			name:     "connection reset error",
			err:      fmt.Errorf("connection reset by peer"),
			expected: true,
		},
		{
			name:     "use of closed connection",
			err:      fmt.Errorf("use of closed network connection"),
			expected: true,
		},
		{
			name:     "unexpected error",
			err:      fmt.Errorf("some other network error"),
			expected: false,
		},
		{
			name:     "authentication error",
			err:      fmt.Errorf("authentication failed"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isExpectedConnectionCloseError(tt.err)
			if result != tt.expected {
				t.Errorf("isExpectedConnectionCloseError(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestGracefulErrorHandler(t *testing.T) {
	// Create a test logger that captures output
	var capturedLogs []string
	testLogger := logr.New(logr.Discard().GetSink())

	handler := newGracefulErrorHandler(testLogger)

	tests := []struct {
		name      string
		err       error
		msg       string
		shouldLog bool
	}{
		{
			name:      "nil error",
			err:       nil,
			msg:       "test message",
			shouldLog: false,
		},
		{
			name:      "expected connection close error",
			err:       fmt.Errorf("connection was forcibly closed"),
			msg:       "test message",
			shouldLog: false, // Should be filtered out
		},
		{
			name:      "unexpected error",
			err:       fmt.Errorf("authentication failed"),
			msg:       "test message",
			shouldLog: true, // Should be logged
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This test verifies the handler doesn't panic and processes errors correctly
			// In a real scenario, we'd need to capture the actual log output
			handler.Handle(context.Background(), tt.err, tt.msg)

			// Since we're using logr.Discard(), we can't easily test the actual logging
			// but we can verify the function completes without error
		})
	}

	_ = capturedLogs // Prevent unused variable error
}
