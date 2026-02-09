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
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	var errs []string
	for _, resource := range v.resources {
		switch resource {
		case "nodes":
			_, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			if err != nil {
				errs = append(errs, fmt.Sprintf("get nodes: %v", err))
			}
		case "namespaces":
			_, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
			if err != nil {
				errs = append(errs, fmt.Sprintf("get namespaces: %v", err))
			}
		case "pods":
			_, err := client.CoreV1().Pods(v.namespace).List(ctx, metav1.ListOptions{})
			if err != nil {
				errs = append(errs, fmt.Sprintf("get pods -n %s: %v", v.namespace, err))
			}
		case "secrets":
			_, err := client.CoreV1().Secrets(v.namespace).List(ctx, metav1.ListOptions{})
			if err != nil {
				errs = append(errs, fmt.Sprintf("get secrets -n %s: %v", v.namespace, err))
			}
		case "configmaps":
			_, err := client.CoreV1().ConfigMaps(v.namespace).List(ctx, metav1.ListOptions{})
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
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	var errs []string
	for _, resource := range v.resources {
		switch resource {
		case "secrets":
			_, err := client.CoreV1().Secrets(v.namespace).List(ctx, metav1.ListOptions{})
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
