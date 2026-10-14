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

package verifiers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	configfake "github.com/openshift/client-go/config/clientset/versioned/fake"
)

func TestNewRouterDiagnosticContext(testContext *testing.T) {
	tests := []struct {
		name      string
		newParent func(context.Context) (context.Context, context.CancelFunc)
		wantErr   error
	}{
		{
			name: "already canceled parent",
			newParent: func(ctx context.Context) (context.Context, context.CancelFunc) {
				parent, cancel := context.WithCancel(ctx)
				cancel()
				return parent, cancel
			},
			wantErr: context.Canceled,
		},
		{
			name: "already expired parent",
			newParent: func(ctx context.Context) (context.Context, context.CancelFunc) {
				return context.WithDeadline(ctx, time.Now().Add(-time.Second))
			},
			wantErr: context.DeadlineExceeded,
		},
	}
	for _, test := range tests {
		testContext.Run(test.name, func(testContext *testing.T) {
			type contextKey struct{}
			parent, cancelParent := test.newParent(context.WithValue(context.Background(), contextKey{}, "diagnostic value"))
			defer cancelParent()
			if err := parent.Err(); err != test.wantErr {
				testContext.Fatalf("parent error = %v, want %v", err, test.wantErr)
			}

			before := time.Now()
			diagnosticCtx, cancel := newRouterDiagnosticContext(parent)
			defer cancel()
			after := time.Now()
			if err := diagnosticCtx.Err(); err != nil {
				testContext.Fatalf("diagnostic context is already canceled: %v", err)
			}
			deadline, ok := diagnosticCtx.Deadline()
			if !ok || deadline.Before(before.Add(30*time.Second)) || deadline.After(after.Add(30*time.Second)) {
				testContext.Fatalf("diagnostic deadline = %v (present: %t), want 30 seconds after creation", deadline, ok)
			}
			if value := diagnosticCtx.Value(contextKey{}); value != "diagnostic value" {
				testContext.Errorf("diagnostic context value = %v, want diagnostic value", value)
			}
			cancel()
			if err := diagnosticCtx.Err(); err != context.Canceled {
				testContext.Errorf("diagnostic context error after cancel = %v, want context.Canceled", err)
			}
		})
	}
}

func TestLogRouterDefaultStateWithClients(testContext *testing.T) {
	tests := []struct {
		name                string
		ingress             []corev1.LoadBalancerIngress
		serviceErr          error
		eventsErr           error
		operatorAbsent      bool
		operatorErr         error
		withoutConfigClient bool
		want                []string
	}{
		{
			name:    "service with external IPs",
			ingress: []corev1.LoadBalancerIngress{{IP: "192.0.2.10"}, {IP: "2001:db8::10"}},
			want:    []string{"loadBalancer.ingress=", "192.0.2.10", "2001:db8::10"},
		},
		{
			name: "service without external IPs",
			want: []string{"loadBalancer.ingress=[]"},
		},
		{
			name:       "service not found",
			serviceErr: apierrors.NewNotFound(schema.GroupResource{Resource: "services"}, "router-default"),
			want:       []string{`failed to get openshift-ingress/router-default Service: services "router-default" not found`},
		},
		{
			name:       "service API error",
			serviceErr: errors.New("service unavailable"),
			want:       []string{"failed to get openshift-ingress/router-default Service: service unavailable"},
		},
		{
			name:      "events API error",
			eventsErr: errors.New("events unavailable"),
			want:      []string{"failed to list openshift-ingress events for router-default: events unavailable"},
		},
		{
			name:           "cluster operator absent",
			operatorAbsent: true,
			want:           []string{`failed to get ingress ClusterOperator: clusteroperators.config.openshift.io "ingress" not found`},
		},
		{
			name:        "cluster operator API error",
			operatorErr: errors.New("operator unavailable"),
			want:        []string{"failed to get ingress ClusterOperator: operator unavailable"},
		},
		{
			name:                "config client unavailable",
			withoutConfigClient: true,
			want:                []string{"loadBalancer.ingress=[]"},
		},
	}
	for _, test := range tests {
		testContext.Run(test.name, func(testContext *testing.T) {
			service := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "openshift-ingress",
					Name:      "router-default",
					Annotations: map[string]string{
						"service.beta.kubernetes.io/azure-pip-name": "router-pip",
						"example.com/load-balancer-mode":            "public",
						"example.com/loadbalancer-owner":            "network-team",
						"example.com/unrelated":                     "unrelated-value",
					},
				},
				Spec:   corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
				Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: test.ingress}},
			}
			event := &corev1.Event{
				ObjectMeta:          metav1.ObjectMeta{Namespace: "openshift-ingress", Name: "router-default.failure"},
				InvolvedObject:      corev1.ObjectReference{Namespace: "openshift-ingress", Name: "router-default", Kind: "Service"},
				Type:                corev1.EventTypeWarning,
				Reason:              "SyncLoadBalancerFailed",
				Source:              corev1.EventSource{Component: "service-controller"},
				ReportingController: "service-controller",
				Count:               2,
				Message:             "Azure CCM throttled the load balancer request",
			}
			kubeClient := fake.NewSimpleClientset(service, event)
			kubeClient.PrependReactor("get", "services", func(action clienttesting.Action) (bool, runtime.Object, error) {
				if action.GetNamespace() != "openshift-ingress" || action.(clienttesting.GetAction).GetName() != "router-default" {
					testContext.Fatalf("unexpected Service request: %#v", action)
				}
				return test.serviceErr != nil, nil, test.serviceErr
			})
			kubeClient.PrependReactor("list", "events", func(action clienttesting.Action) (bool, runtime.Object, error) {
				selector := action.(clienttesting.ListAction).GetListRestrictions().Fields.String()
				if action.GetNamespace() != "openshift-ingress" || selector != "involvedObject.name=router-default" {
					testContext.Fatalf("unexpected events request: namespace=%q selector=%q", action.GetNamespace(), selector)
				}
				return test.eventsErr != nil, nil, test.eventsErr
			})
			operator := &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "ingress"},
				Status: configv1.ClusterOperatorStatus{Conditions: []configv1.ClusterOperatorStatusCondition{
					{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Reason: "AsExpected", Message: "router available"},
					{Type: configv1.OperatorDegraded, Status: configv1.ConditionFalse, Reason: "AsExpected", Message: "router healthy"},
					{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Reason: "Reconciling", Message: "router progressing"},
					{Type: configv1.OperatorUpgradeable, Status: configv1.ConditionTrue, Reason: "AsExpected", Message: "unrelated condition"},
				}},
			}
			var configObjects []runtime.Object
			if !test.operatorAbsent {
				configObjects = append(configObjects, operator)
			}
			fakeConfigClient := configfake.NewSimpleClientset(configObjects...)
			fakeConfigClient.PrependReactor("get", "clusteroperators", func(action clienttesting.Action) (bool, runtime.Object, error) {
				if action.GetNamespace() != "" || action.(clienttesting.GetAction).GetName() != "ingress" {
					testContext.Fatalf("unexpected ClusterOperator request: %#v", action)
				}
				return test.operatorErr != nil, nil, test.operatorErr
			})
			var configClient configclient.Interface = fakeConfigClient
			if test.withoutConfigClient {
				configClient = nil
			}

			var output bytes.Buffer
			logRouterDefaultStateWithClients(context.Background(), kubeClient, configClient, &output)

			if actions := kubeClient.Actions(); len(actions) != 2 {
				testContext.Fatalf("expected both Service and events requests even on API errors, got %#v", actions)
			}
			operatorRequests := 1
			if test.withoutConfigClient {
				operatorRequests = 0
			}
			if actions := fakeConfigClient.Actions(); len(actions) != operatorRequests {
				testContext.Fatalf("expected %d ClusterOperator requests even on Kubernetes API errors, got %#v", operatorRequests, actions)
			}
			want := append([]string{}, test.want...)
			dontWant := []string{"example.com/unrelated", "unrelated-value", "condition=Upgradeable", "unrelated condition"}
			if test.serviceErr == nil {
				want = append(want, "Service: type=LoadBalancer", "service.beta.kubernetes.io/azure-pip-name:router-pip", "example.com/load-balancer-mode:public", "example.com/loadbalancer-owner:network-team")
				dontWant = append(dontWant, "failed to get openshift-ingress/router-default Service")
			} else {
				dontWant = append(dontWant, "Service: type=", "loadBalancerAnnotations=")
			}
			if test.eventsErr == nil {
				want = append(want, "openshift-ingress events for router-default: count=1", "kind=Service type=Warning reason=SyncLoadBalancerFailed source=service-controller reportingController=service-controller count=2", "message=Azure CCM throttled the load balancer request")
				dontWant = append(dontWant, "failed to list openshift-ingress events")
			} else {
				dontWant = append(dontWant, "router-default event:")
			}
			if !test.operatorAbsent && test.operatorErr == nil && !test.withoutConfigClient {
				want = append(want, "condition=Available status=True reason=AsExpected message=router available", "condition=Degraded status=False reason=AsExpected message=router healthy", "condition=Progressing status=True reason=Reconciling message=router progressing")
				dontWant = append(dontWant, "failed to get ingress ClusterOperator")
			} else {
				dontWant = append(dontWant, "ingress ClusterOperator: condition=")
			}
			for _, expected := range want {
				if !strings.Contains(output.String(), expected) {
					testContext.Errorf("expected diagnostic %q in output:\n%s", expected, output.String())
				}
			}
			for _, unexpected := range dontWant {
				if strings.Contains(output.String(), unexpected) {
					testContext.Errorf("unexpected diagnostic %q in output:\n%s", unexpected, output.String())
				}
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func overridePollInterval(t *testing.T) {
	t.Helper()
	orig := routeReachabilityPollInterval
	routeReachabilityPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { routeReachabilityPollInterval = orig })
}

func okResponse() *http.Response {
	return &http.Response{
		StatusCode:    200,
		Status:        "200 OK",
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader("OK")),
		ContentLength: 2,
	}
}

func dnsOpError(host string) *net.OpError {
	return &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: &net.DNSError{
			Err:        "no such host",
			Name:       host,
			IsNotFound: true,
		},
	}
}

func TestWaitForRouteReachability_ImmediateSuccess(t *testing.T) {
	overridePollInterval(t)
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return okResponse(), nil
		}),
	}

	err := waitForRouteReachability(context.Background(), client, "https://example.com", 5*time.Second, "test")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestWaitForRouteReachability_SuccessAfterNon200(t *testing.T) {
	overridePollInterval(t)
	attempts := 0
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts < 3 {
				return &http.Response{
					StatusCode:    503,
					Status:        "503 Service Unavailable",
					Proto:         "HTTP/1.1",
					ProtoMajor:    1,
					ProtoMinor:    1,
					Header:        make(http.Header),
					Body:          io.NopCloser(strings.NewReader("unavailable")),
					ContentLength: 11,
				}, nil
			}
			return okResponse(), nil
		}),
	}

	err := waitForRouteReachability(context.Background(), client, "https://example.com", 5*time.Second, "test")
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if attempts < 3 {
		t.Fatalf("expected at least 3 attempts, got %d", attempts)
	}
}

func TestWaitForRouteReachability_DNSErrorClassification(t *testing.T) {
	overridePollInterval(t)
	attempts := 0
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts < 3 {
				return nil, dnsOpError(req.URL.Hostname())
			}
			return okResponse(), nil
		}),
	}

	err := waitForRouteReachability(context.Background(), client, "https://test.example.com", 5*time.Second, "test")
	if err != nil {
		t.Fatalf("expected success after DNS retries, got: %v", err)
	}
	if attempts < 3 {
		t.Fatalf("expected at least 3 attempts, got %d", attempts)
	}
}

func TestWaitForRouteReachability_DNSErrorThenNonDNSError(t *testing.T) {
	overridePollInterval(t)
	attempts := 0
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			switch {
			case attempts <= 2:
				return nil, dnsOpError(req.URL.Hostname())
			case attempts <= 4:
				return nil, fmt.Errorf("connection reset")
			default:
				return okResponse(), nil
			}
		}),
	}

	err := waitForRouteReachability(context.Background(), client, "https://test.example.com", 5*time.Second, "test")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if attempts < 5 {
		t.Fatalf("expected at least 5 attempts, got %d", attempts)
	}
}

func TestWaitForRouteReachability_TimeoutReturnsLastError(t *testing.T) {
	overridePollInterval(t)
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    503,
				Status:        "503 Service Unavailable",
				Proto:         "HTTP/1.1",
				ProtoMajor:    1,
				ProtoMinor:    1,
				Header:        make(http.Header),
				Body:          io.NopCloser(strings.NewReader("unavailable")),
				ContentLength: 11,
			}, nil
		}),
	}

	err := waitForRouteReachability(context.Background(), client, "https://example.com", 100*time.Millisecond, "test")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "route was never reachable") {
		t.Fatalf("expected 'route was never reachable' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "503") {
		t.Fatalf("expected last status error wrapped, got: %v", err)
	}
}

func TestWaitForRouteReachability_DNSTimeoutReturnsLastError(t *testing.T) {
	overridePollInterval(t)
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, dnsOpError(req.URL.Hostname())
		}),
	}

	err := waitForRouteReachability(context.Background(), client, "https://test.example.com", 100*time.Millisecond, "test")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "route was never reachable") {
		t.Fatalf("expected 'route was never reachable' in error, got: %v", err)
	}
}
