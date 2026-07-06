// Copyright 2026 Microsoft Corporation
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

package framework

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// DeploySampleApp creates a simple web app (agnhost serve-hostname) with a
// Service and Route in a new namespace. Returns the namespace name and the
// route host, or an error.
func DeploySampleApp(ctx context.Context, dynamicClient dynamic.Interface) (string, string, error) {
	// Create namespace
	nsObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"generateName": "e2e-private-ingress-",
			},
		},
	}
	ns, err := dynamicClient.Resource(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}).
		Create(ctx, nsObj, metav1.CreateOptions{})
	if err != nil {
		return "", "", fmt.Errorf("failed to create namespace: %w", err)
	}
	nsName := ns.GetName()

	// Create deployment
	deployment := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name": "agnhost-server",
			},
			"spec": map[string]interface{}{
				"replicas": int64(1),
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{"app": "agnhost"},
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{"app": "agnhost"},
					},
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "agnhost",
								"image": "registry.k8s.io/e2e-test-images/agnhost:2.39",
								"args":  []interface{}{"serve-hostname", "--port=8080"},
								"ports": []interface{}{
									map[string]interface{}{
										"name":          "http",
										"containerPort": int64(8080),
										"protocol":      "TCP",
									},
								},
							},
						},
					},
				},
			},
		},
	}
	_, err = dynamicClient.Resource(schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}).
		Namespace(nsName).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil {
		return nsName, "", fmt.Errorf("failed to create deployment: %w", err)
	}

	// Create service
	service := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name": "agnhost",
			},
			"spec": map[string]interface{}{
				"ports": []interface{}{
					map[string]interface{}{
						"name":       "http",
						"port":       int64(80),
						"targetPort": int64(8080),
						"protocol":   "TCP",
					},
				},
				"selector": map[string]interface{}{"app": "agnhost"},
				"type":     "ClusterIP",
			},
		},
	}
	_, err = dynamicClient.Resource(schema.GroupVersionResource{Version: "v1", Resource: "services"}).
		Namespace(nsName).Create(ctx, service, metav1.CreateOptions{})
	if err != nil {
		return nsName, "", fmt.Errorf("failed to create service: %w", err)
	}

	// Create route
	route := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "route.openshift.io/v1",
			"kind":       "Route",
			"metadata": map[string]interface{}{
				"name": "agnhost",
			},
			"spec": map[string]interface{}{
				"port": map[string]interface{}{
					"targetPort": "http",
				},
				"tls": map[string]interface{}{
					"termination": "edge",
				},
				"to": map[string]interface{}{
					"kind":   "Service",
					"name":   "agnhost",
					"weight": int64(100),
				},
			},
		},
	}
	routeGVR := schema.GroupVersionResource{Group: "route.openshift.io", Version: "v1", Resource: "routes"}
	_, err = dynamicClient.Resource(routeGVR).Namespace(nsName).Create(ctx, route, metav1.CreateOptions{})
	if err != nil {
		return nsName, "", fmt.Errorf("failed to create route: %w", err)
	}

	// Wait for route to get a host
	var host string
	pollCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	for {
		r, err := dynamicClient.Resource(routeGVR).Namespace(nsName).Get(pollCtx, "agnhost", metav1.GetOptions{})
		if err == nil {
			h, _, _ := unstructured.NestedString(r.Object, "spec", "host")
			if h != "" {
				host = h
				break
			}
		}
		select {
		case <-pollCtx.Done():
			return nsName, "", fmt.Errorf("timed out waiting for route host to be assigned")
		case <-time.After(5 * time.Second):
		}
	}

	return nsName, host, nil
}
