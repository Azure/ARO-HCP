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
	"strings"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// PermissionCheck describes a single RBAC permission to verify via SelfSubjectAccessReview.
type PermissionCheck struct {
	Group       string
	Resource    string
	Verb        string
	Namespace   string // empty for cluster-scoped
	SubResource string // empty if not a subresource
}

func (p PermissionCheck) String() string {
	parts := []string{p.Verb, p.Group + "/" + p.Resource}
	if p.SubResource != "" {
		parts[1] += "/" + p.SubResource
	}
	if p.Namespace != "" {
		parts = append(parts, "in", p.Namespace)
	}
	return strings.Join(parts, " ")
}

// CanList returns a PermissionCheck for the list verb on a cluster-scoped resource.
func CanList(group, resource string) PermissionCheck {
	return PermissionCheck{Group: group, Resource: resource, Verb: "list"}
}

// CanGet returns a PermissionCheck for the get verb on a cluster-scoped resource.
func CanGet(group, resource string) PermissionCheck {
	return PermissionCheck{Group: group, Resource: resource, Verb: "get"}
}

// CanWatch returns a PermissionCheck for the watch verb on a cluster-scoped resource.
func CanWatch(group, resource string) PermissionCheck {
	return PermissionCheck{Group: group, Resource: resource, Verb: "watch"}
}

// CanCreate returns a PermissionCheck for the create verb on a cluster-scoped resource.
func CanCreate(group, resource string) PermissionCheck {
	return PermissionCheck{Group: group, Resource: resource, Verb: "create"}
}

// CanDelete returns a PermissionCheck for the delete verb on a cluster-scoped resource.
func CanDelete(group, resource string) PermissionCheck {
	return PermissionCheck{Group: group, Resource: resource, Verb: "delete"}
}

// CanUpdate returns a PermissionCheck for the update verb on a cluster-scoped resource.
func CanUpdate(group, resource string) PermissionCheck {
	return PermissionCheck{Group: group, Resource: resource, Verb: "update"}
}

// CanGetSubresource returns a PermissionCheck for reading a subresource (e.g. pods/log).
func CanGetSubresource(group, resource, subresource string) PermissionCheck {
	return PermissionCheck{Group: group, Resource: resource, Verb: "get", SubResource: subresource}
}

// VerifyRBACAllowed returns a verifier that asserts all given permission checks are allowed
// according to the cluster's RBAC rules, using SelfSubjectAccessReview.
func VerifyRBACAllowed(checks ...PermissionCheck) HostedClusterVerifier {
	return verifyPermissions{checks: checks}
}

type verifyPermissions struct {
	checks []PermissionCheck
}

func (v verifyPermissions) Name() string {
	names := make([]string, len(v.checks))
	for i, c := range v.checks {
		names[i] = c.String()
	}
	return fmt.Sprintf("VerifyRBACAllowed(%s)", strings.Join(names, "; "))
}

func (v verifyPermissions) Verify(ctx context.Context, restConfig *rest.Config) error {
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	var errs []error
	for _, check := range v.checks {
		review := &authorizationv1.SelfSubjectAccessReview{
			Spec: authorizationv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Namespace:   check.Namespace,
					Verb:        check.Verb,
					Group:       check.Group,
					Resource:    check.Resource,
					Subresource: check.SubResource,
				},
			},
		}
		result, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
		if err != nil {
			errs = append(errs, fmt.Errorf("SSAR failed for %s: %w", check, err))
			continue
		}
		if !result.Status.Allowed {
			errs = append(errs, fmt.Errorf("expected allowed but denied: %s (reason: %s)", check, result.Status.Reason))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("permission checks failed: %w", errors.Join(errs...))
	}
	return nil
}

// VerifyRBACDenied returns a verifier that asserts all given permission checks are denied
// according to the cluster's RBAC rules, using SelfSubjectAccessReview.
func VerifyRBACDenied(checks ...PermissionCheck) HostedClusterVerifier {
	return verifyPermissionsDenied{checks: checks}
}

type verifyPermissionsDenied struct {
	checks []PermissionCheck
}

func (v verifyPermissionsDenied) Name() string {
	names := make([]string, len(v.checks))
	for i, c := range v.checks {
		names[i] = c.String()
	}
	return fmt.Sprintf("VerifyRBACDenied(%s)", strings.Join(names, "; "))
}

func (v verifyPermissionsDenied) Verify(ctx context.Context, restConfig *rest.Config) error {
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	var errs []error
	for _, check := range v.checks {
		review := &authorizationv1.SelfSubjectAccessReview{
			Spec: authorizationv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Namespace:   check.Namespace,
					Verb:        check.Verb,
					Group:       check.Group,
					Resource:    check.Resource,
					Subresource: check.SubResource,
				},
			},
		}
		result, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
		if err != nil {
			errs = append(errs, fmt.Errorf("SSAR failed for %s: %w", check, err))
			continue
		}
		if result.Status.Allowed {
			errs = append(errs, fmt.Errorf("expected denied but allowed: %s", check))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("permission denied checks failed: %w", errors.Join(errs...))
	}
	return nil
}
