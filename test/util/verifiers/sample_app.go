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

package verifiers

import (
	"context"
	"fmt"
	"time"

	"go.yaml.in/yaml/v2"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

// SampleAppDeployment holds the resources created by DeploySampleApp.
type SampleAppDeployment struct {
	Namespace string
	RouteHost string
}

// DeploySampleApp creates a simple web app (agnhost serve-hostname) with a
// Service and Route in a new namespace on the given cluster. It returns the
// namespace name and the route host once the route is assigned a hostname.
// The nodeSelector parameter is optional; if provided, the first map is applied
// to the deployment's pod template.
func DeploySampleApp(ctx context.Context, adminRESTConfig *rest.Config, nodeSelector ...map[string]string) (*SampleAppDeployment, error) {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	namespace, err := kubeClient.CoreV1().Namespaces().Create(
		ctx,
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "e2e-sample-app-",
			},
		},
		metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create namespace: %w", err)
	}
	nsName := namespace.Name

	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	deploymentYAML := must(staticFiles.ReadFile("artifacts/serving_app/deployment.yaml"))

	if len(nodeSelector) > 0 && nodeSelector[0] != nil {
		var deploymentMap map[string]any
		if err := yaml.Unmarshal(deploymentYAML, &deploymentMap); err != nil {
			return nil, fmt.Errorf("failed to unmarshal deployment YAML: %w", err)
		}

		if spec, ok := deploymentMap["spec"].(map[string]any); ok {
			if template, ok := spec["template"].(map[string]any); ok {
				if templateSpec, ok := template["spec"].(map[string]any); ok {
					templateSpec["nodeSelector"] = nodeSelector[0]
				}
			}
		}

		deploymentYAML, err = yaml.Marshal(deploymentMap)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal modified deployment: %w", err)
		}
	}

	deployment, err := createArbitraryResource(ctx, dynamicClient, nsName, deploymentYAML)
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment: %w", err)
	}
	service, err := createArbitraryResource(ctx, dynamicClient, nsName, must(staticFiles.ReadFile("artifacts/serving_app/service.yaml")))
	if err != nil {
		return nil, fmt.Errorf("failed to create service: %w", err)
	}
	route, err := createArbitraryResource(ctx, dynamicClient, nsName, must(staticFiles.ReadFile("artifacts/serving_app/route.yaml")))
	if err != nil {
		return nil, fmt.Errorf("failed to create route: %w", err)
	}
	klog.InfoS("created sample app resources",
		"namespace", nsName,
		"deployment", deployment.GetName(),
		"service", service.GetName(),
		"route", route.GetName(),
	)

	// Wait for route to get a hostname
	var host string
	var lastErr error
	err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 5*time.Minute, true, func(ctx context.Context) (done bool, err error) {
		currRoute, err := dynamicClient.Resource(gvr("route.openshift.io", "v1", "routes")).
			Namespace(nsName).Get(ctx, route.GetName(), metav1.GetOptions{})
		if err != nil {
			lastErr = err
			return false, nil
		}
		host, _, _ = unstructured.NestedString(currRoute.Object, "spec", "host")
		return len(host) > 0, nil
	})
	if err != nil {
		if lastErr != nil {
			return nil, fmt.Errorf("timed out waiting for route host to be assigned: %w", lastErr)
		}
		return nil, fmt.Errorf("timed out waiting for route host to be assigned: %w", err)
	}

	return &SampleAppDeployment{
		Namespace: nsName,
		RouteHost: host,
	}, nil
}
