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
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type verifyWhoAmI struct {
	expectedGroups []string
}

func VerifyWhoAmI(expectedGroups ...string) HostedClusterVerifier {
	return verifyWhoAmI{expectedGroups: expectedGroups}
}

func (v verifyWhoAmI) Name() string {
	return fmt.Sprintf("VerifyWhoAmI(groups=%s)", strings.Join(v.expectedGroups, ","))
}

func (v verifyWhoAmI) Verify(ctx context.Context, restConfig *rest.Config) error {
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	review, err := client.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("whoami failed: %w", err)
	}

	userInfo := review.Status.UserInfo
	actualGroups := make(map[string]bool)
	for _, g := range userInfo.Groups {
		actualGroups[g] = true
	}

	var missingGroups []string
	for _, expected := range v.expectedGroups {
		if !actualGroups[expected] {
			missingGroups = append(missingGroups, expected)
		}
	}

	if len(missingGroups) > 0 {
		return fmt.Errorf("user %q is missing expected groups: %v (actual groups: %v)",
			userInfo.Username, missingGroups, userInfo.Groups)
	}

	return nil
}

type verifyCanRead struct {
	namespace string
	resources []string
}

func VerifyList(resources ...string) HostedClusterVerifier {
	return verifyCanRead{namespace: "", resources: resources}
}

func VerifyListNamespaced(namespace string, resources ...string) HostedClusterVerifier {
	return verifyCanRead{namespace: namespace, resources: resources}
}

func (v verifyCanRead) Name() string {
	if v.namespace == "" {
		return fmt.Sprintf("VerifyList(resources=%s)", strings.Join(v.resources, ","))
	}
	return fmt.Sprintf("VerifyListNamespaced(namespace=%s, resources=%s)", v.namespace, strings.Join(v.resources, ","))
}

func (v verifyCanRead) Verify(ctx context.Context, restConfig *rest.Config) error {
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	var errs []error
	for _, resource := range v.resources {
		switch resource {
		case "nodes":
			_, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			if err != nil {
				errs = append(errs, fmt.Errorf("get nodes: %w", err))
			}
		case "namespaces":
			_, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
			if err != nil {
				errs = append(errs, fmt.Errorf("get namespaces: %w", err))
			}
		case "pods":
			_, err := client.CoreV1().Pods(v.namespace).List(ctx, metav1.ListOptions{})
			if err != nil {
				errs = append(errs, fmt.Errorf("get pods -n %s: %w", v.namespace, err))
			}
		case "secrets":
			_, err := client.CoreV1().Secrets(v.namespace).List(ctx, metav1.ListOptions{})
			if err != nil {
				errs = append(errs, fmt.Errorf("get secrets -n %s: %w", v.namespace, err))
			}
		case "configmaps":
			_, err := client.CoreV1().ConfigMaps(v.namespace).List(ctx, metav1.ListOptions{})
			if err != nil {
				errs = append(errs, fmt.Errorf("get configmaps -n %s: %w", v.namespace, err))
			}
		default:
			errs = append(errs, fmt.Errorf("unknown resource: %s", resource))
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("read access verification failed: %w", errors.Join(errs...))
}

type verifyCanGetDeploymentLogs struct {
	namespace      string
	deploymentName string
	containerName  string
}

// VerifyGetDeploymentLogs returns a verifier that checks the identity can read logs from
// a pod of the given deployment. It discovers one pod via the deployment's label selector
// and fetches logs from it. containerName is optional; leave empty for the pod's default container.
func VerifyGetDeploymentLogs(namespace, deploymentName, containerName string) HostedClusterVerifier {
	return verifyCanGetDeploymentLogs{
		namespace:      namespace,
		deploymentName: deploymentName,
		containerName:  containerName,
	}
}

func (v verifyCanGetDeploymentLogs) Name() string {
	if v.containerName == "" {
		return fmt.Sprintf("VerifyGetDeploymentLogs(namespace=%s, deployment=%s)", v.namespace, v.deploymentName)
	}
	return fmt.Sprintf("VerifyGetDeploymentLogs(namespace=%s, deployment=%s, container=%s)", v.namespace, v.deploymentName, v.containerName)
}

func (v verifyCanGetDeploymentLogs) Verify(ctx context.Context, restConfig *rest.Config) error {
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	deployment, err := client.AppsV1().Deployments(v.namespace).Get(ctx, v.deploymentName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get deployment %s -n %s: %w", v.deploymentName, v.namespace, err)
	}

	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		return fmt.Errorf("deployment %s selector: %w", v.deploymentName, err)
	}

	pods, err := client.CoreV1().Pods(v.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return fmt.Errorf("list pods for deployment %s -n %s: %w", v.deploymentName, v.namespace, err)
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("deployment %s -n %s has no pods", v.deploymentName, v.namespace)
	}

	// Use first running pod
	var podName, containerName string
	for _, p := range pods.Items {
		if p.Status.Phase == corev1.PodRunning {
			podName = p.Name
			containerName = p.Spec.Containers[0].Name
			break
		}
	}
	if podName == "" {
		return fmt.Errorf("deployment %s -n %s has no running pods", v.deploymentName, v.namespace)
	}

	opts := &corev1.PodLogOptions{
		Container: containerName,
	}
	req := client.CoreV1().Pods(v.namespace).GetLogs(podName, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("get logs -n %s %s (deployment %s): %w", v.namespace, podName, v.deploymentName, err)
	}
	defer stream.Close()
	_, err = io.Copy(io.Discard, stream)
	if err != nil {
		return fmt.Errorf("read logs -n %s %s (deployment %s): %w", v.namespace, podName, v.deploymentName, err)
	}
	return nil
}
