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

	"google.golang.org/protobuf/proto"
	securityv1beta1api "istio.io/api/security/v1beta1"
	typev1beta1 "istio.io/api/type/v1beta1"
	securityv1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	metaapplyv1 "istio.io/client-go/pkg/applyconfiguration/meta/v1"
	securityapplyv1beta1 "istio.io/client-go/pkg/applyconfiguration/security/v1beta1"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/server"
)

func authorizationPolicyNameForSession(session *sessiongatev1alpha1.Session) string {
	return session.Name
}

func buildAuthorizationPolicySpec(session *sessiongatev1alpha1.Session) securityv1beta1api.AuthorizationPolicy {
	return securityv1beta1api.AuthorizationPolicy{
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
								fmt.Sprintf("%s/*", server.BuildSessionKASUrlPath(session.Name)), // todo: use endpoint provider
							},
						},
					},
				},
				When: []*securityv1beta1api.Condition{
					{
						Key:    fmt.Sprintf("request.auth.claims[%s]", session.Spec.Owner.UserPrincipal.Claim),
						Values: []string{session.Spec.Owner.UserPrincipal.Name},
					},
				},
			},
		},
	}
}

func buildAuthorizationPolicyApplyConfiguration(session *sessiongatev1alpha1.Session) *securityapplyv1beta1.AuthorizationPolicyApplyConfiguration {
	return securityapplyv1beta1.AuthorizationPolicy(session.Name, session.Namespace).
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
			buildAuthorizationPolicySpec(session),
		)
}

func authorizationPolicyUpdateNeeded(current *securityv1beta1.AuthorizationPolicy, desired *securityapplyv1beta1.AuthorizationPolicyApplyConfiguration) bool {
	if current == nil {
		return true
	}

	for k, v := range desired.Labels {
		if current.Labels[k] != v {
			return true
		}
	}

	return !proto.Equal(desired.Spec, &current.Spec)
}
