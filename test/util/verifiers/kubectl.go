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
	"fmt"
	"strings"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// KubectlRunner provides kubectl-like operations using a rest.Config.
type KubectlRunner struct {
	restConfig *rest.Config
	client     *kubernetes.Clientset
}

// NewKubectlRunner creates a new KubectlRunner from a rest.Config.
func NewKubectlRunner(restConfig *rest.Config) (*KubectlRunner, error) {
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	return &KubectlRunner{
		restConfig: restConfig,
		client:     client,
	}, nil
}

// GetPods lists pods in a namespace.
func (k *KubectlRunner) GetPods(ctx context.Context, namespace string) (*corev1.PodList, error) {
	return k.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
}

// GetNodes lists all nodes in the cluster.
func (k *KubectlRunner) GetNodes(ctx context.Context) (*corev1.NodeList, error) {
	return k.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
}

// GetNamespaces lists all namespaces in the cluster.
func (k *KubectlRunner) GetNamespaces(ctx context.Context) (*corev1.NamespaceList, error) {
	return k.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
}

// GetSecrets lists secrets in a namespace.
func (k *KubectlRunner) GetSecrets(ctx context.Context, namespace string) (*corev1.SecretList, error) {
	return k.client.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{})
}

// GetConfigMaps lists configmaps in a namespace.
func (k *KubectlRunner) GetConfigMaps(ctx context.Context, namespace string) (*corev1.ConfigMapList, error) {
	return k.client.CoreV1().ConfigMaps(namespace).List(ctx, metav1.ListOptions{})
}

// CreateConfigMap creates a configmap in a namespace.
func (k *KubectlRunner) CreateConfigMap(ctx context.Context, namespace, name string, data map[string]string) (*corev1.ConfigMap, error) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: data,
	}
	return k.client.CoreV1().ConfigMaps(namespace).Create(ctx, cm, metav1.CreateOptions{})
}

func (k *KubectlRunner) DeleteConfigMap(ctx context.Context, namespace, name string) error {
	return k.client.CoreV1().ConfigMaps(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

func (k *KubectlRunner) WhoAmI(ctx context.Context) (*authenticationv1.SelfSubjectReview, error) {
	return k.client.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
}

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
	runner, err := NewKubectlRunner(restConfig)
	if err != nil {
		return err
	}

	review, err := runner.WhoAmI(ctx)
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

func VerifyCanRead(resources ...string) HostedClusterVerifier {
	return verifyCanRead{namespace: "", resources: resources}
}

func VerifyCanReadNamespaced(namespace string, resources ...string) HostedClusterVerifier {
	return verifyCanRead{namespace: namespace, resources: resources}
}

func (v verifyCanRead) Name() string {
	if v.namespace == "" {
		return fmt.Sprintf("VerifyCanRead(resources=%s)", strings.Join(v.resources, ","))
	}
	return fmt.Sprintf("VerifyCanRead(namespace=%s, resources=%s)", v.namespace, strings.Join(v.resources, ","))
}

func (v verifyCanRead) Verify(ctx context.Context, restConfig *rest.Config) error {
	runner, err := NewKubectlRunner(restConfig)
	if err != nil {
		return err
	}

	var errs []string
	for _, resource := range v.resources {
		switch resource {
		case "nodes":
			_, err := runner.GetNodes(ctx)
			if err != nil {
				errs = append(errs, fmt.Sprintf("get nodes: %v", err))
			}
		case "namespaces":
			_, err := runner.GetNamespaces(ctx)
			if err != nil {
				errs = append(errs, fmt.Sprintf("get namespaces: %v", err))
			}
		case "pods":
			_, err := runner.GetPods(ctx, v.namespace)
			if err != nil {
				errs = append(errs, fmt.Sprintf("get pods -n %s: %v", v.namespace, err))
			}
		case "secrets":
			_, err := runner.GetSecrets(ctx, v.namespace)
			if err != nil {
				errs = append(errs, fmt.Sprintf("get secrets -n %s: %v", v.namespace, err))
			}
		case "configmaps":
			_, err := runner.GetConfigMaps(ctx, v.namespace)
			if err != nil {
				errs = append(errs, fmt.Sprintf("get configmaps -n %s: %v", v.namespace, err))
			}
		default:
			errs = append(errs, fmt.Sprintf("unknown resource: %s", resource))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("read access verification failed:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

type verifyCannotRead struct {
	namespace string
	resources []string
}

func VerifyCannotRead(resources ...string) HostedClusterVerifier {
	return verifyCannotRead{namespace: "", resources: resources}
}

func VerifyCannotReadNamespaced(namespace string, resources ...string) HostedClusterVerifier {
	return verifyCannotRead{namespace: namespace, resources: resources}
}

func (v verifyCannotRead) Name() string {
	if v.namespace == "" {
		return fmt.Sprintf("VerifyCannotRead(resources=%s)", strings.Join(v.resources, ","))
	}
	return fmt.Sprintf("VerifyCannotRead(namespace=%s, resources=%s)", v.namespace, strings.Join(v.resources, ","))
}

func (v verifyCannotRead) Verify(ctx context.Context, restConfig *rest.Config) error {
	runner, err := NewKubectlRunner(restConfig)
	if err != nil {
		return err
	}

	var errs []string
	for _, resource := range v.resources {
		switch resource {
		case "secrets":
			_, err := runner.GetSecrets(ctx, v.namespace)
			if err == nil {
				errs = append(errs, fmt.Sprintf("get secrets -n %s: expected access denied, but succeeded", v.namespace))
			}
		default:
			errs = append(errs, fmt.Sprintf("unknown resource for deny check: %s", resource))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("access denial verification failed:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}
