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

package controller

import (
	"fmt"

	securityv1beta1api "istio.io/api/security/v1beta1"
	typev1beta1 "istio.io/api/type/v1beta1"
	metaapplyv1 "istio.io/client-go/pkg/applyconfiguration/meta/v1"
	securityapplyv1beta1 "istio.io/client-go/pkg/applyconfiguration/security/v1beta1"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
)

func authorizationPolicyNameForSession(session *sessiongatev1alpha1.Session) string {
	return session.Name
}

func buildAuthorizationPolicy(session *sessiongatev1alpha1.Session) *securityapplyv1beta1.AuthorizationPolicyApplyConfiguration {
	claim := session.Spec.Owner.UserPrincipal.Claim
	principal := session.Spec.Owner.UserPrincipal.Name
	policyCfg := securityapplyv1beta1.AuthorizationPolicy(session.Name, session.Namespace).
		WithOwnerReferences(metaapplyv1.OwnerReference().
			WithBlockOwnerDeletion(true).
			WithAPIVersion(sessiongatev1alpha1.SchemeGroupVersion.String()).
			WithKind("Session").
			WithName(authorizationPolicyNameForSession(session)).
			WithUID(session.UID).
			WithController(true)).
		WithLabels(map[string]string{
			LabelManagedBy: ControllerAgentName,
		}).
		WithSpec(
			securityv1beta1api.AuthorizationPolicy{
				Selector: &typev1beta1.WorkloadSelector{
					MatchLabels: map[string]string{
						"app.kubernetes.io/name": "sessiongate",
					},
				},
				Action: securityv1beta1api.AuthorizationPolicy_ALLOW,
				Rules: []*securityv1beta1api.Rule{
					{
						To: []*securityv1beta1api.Rule_To{
							{
								Operation: &securityv1beta1api.Operation{
									Paths: []string{
										fmt.Sprintf("/sessiongate/%s/kas/*", session.Name), // todo: use endpoint provider
									},
								},
							},
						},
						When: []*securityv1beta1api.Condition{
							{
								Key:    fmt.Sprintf("request.auth.claims[%s]", claim),
								Values: []string{principal},
							},
						},
					},
				},
			},
		)
	return policyCfg
}
