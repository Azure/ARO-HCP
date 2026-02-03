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
	if session.Status.AuthorizationPolicyRef != "" {
		return session.Status.AuthorizationPolicyRef
	}
	return fmt.Sprintf("sessiongate-%s", getDeterministicSuffixForSession(session.Namespace, session.Name))
}

func buildAuthorizationPolicySpec(session *sessiongatev1alpha1.Session) (securityv1beta1api.AuthorizationPolicy, error) {
	conditions, err := buildClaimConditionsForPrincipal(session.Spec.Owner)
	if err != nil {
		return securityv1beta1api.AuthorizationPolicy{}, fmt.Errorf("failed to build claim conditions for principal: %w", err)
	}
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
								fmt.Sprintf("%s/*", server.BuildSessionKASUrlPath(session.Name)),
							},
						},
					},
				},
				When: conditions,
			},
		},
	}, nil
}

// buildClaimConditionsForPrincipal derives JWT claim conditions from the principal's identity type and name.
func buildClaimConditionsForPrincipal(owner sessiongatev1alpha1.Principal) ([]*securityv1beta1api.Condition, error) {
	var claimName string
	switch owner.Type {
	case sessiongatev1alpha1.PrincipalTypeAzureUser:
		claimName = "upn"
	case sessiongatev1alpha1.PrincipalTypeAzureServicePrincipal:
		claimName = "appid"
	default:
		return nil, fmt.Errorf("unexpected identity type: %s, supports only azureUser and azureServicePrincipal", owner.Type)
	}

	return []*securityv1beta1api.Condition{
		{
			Key:    fmt.Sprintf("request.auth.claims[%s]", claimName),
			Values: []string{owner.Name},
		},
	}, nil
}

func buildAuthorizationPolicyApplyConfiguration(session *sessiongatev1alpha1.Session) (*securityapplyv1beta1.AuthorizationPolicyApplyConfiguration, error) {
	spec, err := buildAuthorizationPolicySpec(session)
	if err != nil {
		return nil, fmt.Errorf("failed to build authorization policy spec: %w", err)
	}
	return securityapplyv1beta1.AuthorizationPolicy(authorizationPolicyNameForSession(session), session.Namespace).
		WithOwnerReferences(metaapplyv1.OwnerReference().
			WithBlockOwnerDeletion(true).
			WithAPIVersion(sessiongatev1alpha1.SchemeGroupVersion.String()).
			WithKind("Session").
			WithName(session.Name).
			WithUID(session.UID).
			WithController(true)).
		WithLabels(map[string]string{
			LabelManagedBy: ControllerAgentName,
		}).
		WithSpec(
			spec,
		), nil
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
