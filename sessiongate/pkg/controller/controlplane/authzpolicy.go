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

package controlplane

import (
	"context"
	"fmt"

	securityv1beta1 "istio.io/api/security/v1beta1"
	typev1beta1 "istio.io/api/type/v1beta1"
	metaapplyv1 "istio.io/client-go/pkg/applyconfiguration/meta/v1"
	securityapplyv1beta1 "istio.io/client-go/pkg/applyconfiguration/security/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/controller"
)

// buildAuthorizationPolicyApplyConfig creates an ApplyConfiguration for Server-Side Apply.
// this declaratively describes the desired state of the AuthorizationPolicy.
func buildAuthorizationPolicyApplyConfig(session *sessiongatev1alpha1.Session, namespace string) (*securityapplyv1beta1.AuthorizationPolicyApplyConfiguration, error) {
	policyName := fmt.Sprintf("session-%s", session.Name)

	if session.Spec.Owner.Type != sessiongatev1alpha1.PrincipalTypeUser {
		// right now we only support users but the CRD has room for other types
		return nil, fmt.Errorf("unsupported principal type: %s", session.Spec.Owner.Type)
	}

	// extract claim and principal from session owner
	claim := session.Spec.Owner.UserPrincipal.Claim
	principal := session.Spec.Owner.UserPrincipal.Name

	// NOTE: nested JWT claims are currently not supported. the claim field should reference
	// a top-level claim in the JWT payload. istio's bracket notation treats everything inside
	// [brackets] as a literal claim name. Nested claims would look like this:
	// request.auth.claims[parent][child]

	// Build the authorization policy spec
	spec := securityv1beta1.AuthorizationPolicy{
		Selector: &typev1beta1.WorkloadSelector{
			MatchLabels: map[string]string{
				"app.kubernetes.io/name": "sessiongate",
			},
		},
		Action: securityv1beta1.AuthorizationPolicy_ALLOW,
		Rules: []*securityv1beta1.Rule{
			{
				To: []*securityv1beta1.Rule_To{
					{
						Operation: &securityv1beta1.Operation{
							Paths: []string{
								fmt.Sprintf("/sessiongate/%s/kas/*", session.Name),
							},
						},
					},
				},
				When: []*securityv1beta1.Condition{
					{
						Key:    fmt.Sprintf("request.auth.claims[%s]", claim),
						Values: []string{principal},
					},
				},
			},
		},
	}

	// Build ApplyConfiguration using fluent builder pattern
	//nolint:govet // copylocks: protobuf message contains a mutex internally; builder copies it
	applyConfig := securityapplyv1beta1.AuthorizationPolicy(policyName, namespace).
		WithLabels(map[string]string{
			controller.LabelManagedBy: controller.ControllerAgentName,
		}).
		WithOwnerReferences(
			metaapplyv1.OwnerReference().
				WithAPIVersion(sessiongatev1alpha1.SchemeGroupVersion.String()).
				WithKind("Session").
				WithName(session.Name).
				WithUID(session.UID).
				WithBlockOwnerDeletion(true).
				WithController(true),
		).
		WithSpec(spec)

	return applyConfig, nil
}

// EnsureAuthorizationPolicy ensures an AuthorizationPolicy exists for the Session
// and matches the expected configuration using SSA
// returns true if the policy was created or updated, false if no change was needed.
func (c *Controller) ensureAuthorizationPolicy(ctx context.Context, session *sessiongatev1alpha1.Session) (bool, error) {
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "session", klog.KObj(session))

	applyConfig, err := buildAuthorizationPolicyApplyConfig(session, c.sessionNamespace)
	policyName := *applyConfig.Name
	if err != nil {
		return false, fmt.Errorf("failed to build AuthorizationPolicy ApplyConfiguration: %w", err)
	}

	// if the policy exists, check if it is owned by the session
	existingPolicy, err := c.authzPoliciesLister.AuthorizationPolicies(c.sessionNamespace).Get(policyName)
	if err == nil {
		if !metav1.IsControlledBy(existingPolicy, session) {
			return false, fmt.Errorf("AuthorizationPolicy is not owned by the session")
		}
	}
	policyExists := err == nil

	policy, err := c.istioclientset.AuthorizationPolicies(c.sessionNamespace).Apply(
		ctx,
		applyConfig,
		metav1.ApplyOptions{
			FieldManager: controller.ControllerAgentName,
		},
	)
	if err != nil {
		return false, fmt.Errorf("failed to apply AuthorizationPolicy: %w", err)
	}

	// determine if a change was made
	changed := !policyExists || existingPolicy.ResourceVersion != policy.ResourceVersion

	if changed {
		logger.V(2).Info("AuthorizationPolicy applied with changes",
			"policyName", policyName,
			"created", !policyExists,
			"uid", policy.UID,
			"resourceVersion", policy.ResourceVersion)
	} else {
		logger.V(6).Info("AuthorizationPolicy unchanged",
			"policyName", policyName,
			"resourceVersion", policy.ResourceVersion)
	}

	return changed, nil
}
