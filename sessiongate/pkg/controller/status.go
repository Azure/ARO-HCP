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
	"time"

	applyv1 "k8s.io/client-go/applyconfigurations/meta/v1"

	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	sessiongatv1alpha1applyconfigurations "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/applyconfiguration/sessiongate/v1alpha1"
)

type ConditionType string

const (
	ConditionTypeReady                        ConditionType = "Ready"
	ConditionTypeSessionActive                ConditionType = "SessionActive"
	ConditionTypeProgressing                  ConditionType = "Progressing"
	ConditionTypeCredentialsAvailable         ConditionType = "CredentialsAvailable"
	ConditionTypeAuthorizationPolicyAvailable ConditionType = "AuthorizationPolicyAvailable"
	ConditionTypeNetworkPathAvailable         ConditionType = "NetworkPathAvailable"
)

type Status struct {
	applyConfig *sessiongatv1alpha1applyconfigurations.SessionStatusApplyConfiguration
}

func NewStatus(status sessiongatev1alpha1.SessionStatus) *Status {
	return &Status{
		applyConfig: ApplyConfigForStatus(status),
	}
}

func (s *Status) WithConditions(updated ...*applyv1.ConditionApplyConfiguration) *Status {
	updatedTypes := sets.New[string]()
	for _, condition := range updated {
		if condition.Type == nil {
			panic(fmt.Errorf("programmer error: must set a type for condition: %#v", condition))
		}
		updatedTypes.Insert(*condition.Type)
	}
	conditions := make([]*applyv1.ConditionApplyConfiguration, 0, len(updated)+len(s.applyConfig.Conditions))
	conditions = append(conditions, updated...)
	for _, condition := range s.applyConfig.Conditions {
		if !updatedTypes.Has(*condition.Type) {
			conditions = append(conditions, &condition)
		}
	}
	// Clear existing conditions and set the new merged list
	s.applyConfig.Conditions = nil
	s.applyConfig.WithConditions(conditions...)
	return s
}

func (s *Status) WithCSRRef(ref string) *Status {
	s.applyConfig.WithCSRRef(ref)
	return s
}

func (s *Status) WithCredentialsSecretRef(ref string) *Status {
	s.applyConfig.WithCredentialsSecretRef(ref)
	return s
}

func (s *Status) WithExpiresAt(expiresAt metav1.Time) *Status {
	s.applyConfig.WithExpiresAt(expiresAt)
	return s
}

func (s *Status) WithEndpoint(endpoint string) *Status {
	s.applyConfig.WithEndpoint(endpoint)
	return s
}

func (s *Status) WithAuthorizationPolicyRef(ref string) *Status {
	s.applyConfig.WithAuthorizationPolicyRef(ref)
	return s
}

func (s *Status) WithBackendKASURL(url string) *Status {
	s.applyConfig.WithBackendKASURL(url)
	return s
}

// AsApplyConfiguration returns the apply configuration for the status and a boolean indicating if the status needs to be updated
// to determine if an update is needed, it compares the SessionStatusApplyConfiguration with the provided session status
func (s *Status) AsApplyConfiguration(session *sessiongatev1alpha1.Session) (*sessiongatv1alpha1applyconfigurations.SessionApplyConfiguration, bool) {
	needsUpdate := false

	// Compare ExpiresAt (only needs to be set once, immutable after that)
	if s.applyConfig.ExpiresAt != nil && session.Status.ExpiresAt == nil {
		needsUpdate = true
	}

	// Compare Endpoint
	if (s.applyConfig.Endpoint != nil && *s.applyConfig.Endpoint != session.Status.Endpoint) || (s.applyConfig.Endpoint == nil && session.Status.Endpoint != "") {
		needsUpdate = true
	}

	// Compare AuthorizationPolicyRef
	if (s.applyConfig.AuthorizationPolicyRef != nil && *s.applyConfig.AuthorizationPolicyRef != session.Status.AuthorizationPolicyRef) || (s.applyConfig.AuthorizationPolicyRef == nil && session.Status.AuthorizationPolicyRef != "") {
		needsUpdate = true
	}

	// Compare CredentialsSecretRef
	if (s.applyConfig.CredentialsSecretRef != nil && *s.applyConfig.CredentialsSecretRef != session.Status.CredentialsSecretRef) || (s.applyConfig.CredentialsSecretRef == nil && session.Status.CredentialsSecretRef != "") {
		needsUpdate = true
	}

	// Compare BackendKASURL
	if (s.applyConfig.BackendKASURL != nil && *s.applyConfig.BackendKASURL != session.Status.BackendKASURL) || (s.applyConfig.BackendKASURL == nil && session.Status.BackendKASURL != "") {
		needsUpdate = true
	}

	// Compare Conditions (ignoring timestamps)
	if !conditionsEqual(s.applyConfig.Conditions, session.Status.Conditions) {
		needsUpdate = true
	}

	cfg := sessiongatv1alpha1applyconfigurations.Session(session.Name, session.Namespace)
	cfg.Status = s.applyConfig
	return cfg, needsUpdate
}

// conditionsEqual compares two sets of conditions, ignoring timestamps
func conditionsEqual(applyConditions []applyv1.ConditionApplyConfiguration, statusConditions []metav1.Condition) bool {
	if len(applyConditions) != len(statusConditions) {
		return false
	}

	// Create a map of apply conditions by type for easy lookup
	applyMap := make(map[string]*applyv1.ConditionApplyConfiguration)
	for i := range applyConditions {
		if applyConditions[i].Type != nil {
			applyMap[*applyConditions[i].Type] = &applyConditions[i]
		}
	}

	// Compare each status condition with the corresponding apply condition
	for _, statusCond := range statusConditions {
		applyCond, exists := applyMap[statusCond.Type]
		if !exists {
			return false
		}

		// Compare fields, ignoring LastTransitionTime
		if applyCond.Status == nil || *applyCond.Status != statusCond.Status {
			return false
		}
		if applyCond.Reason == nil || *applyCond.Reason != statusCond.Reason {
			return false
		}
		if applyCond.Message == nil || *applyCond.Message != statusCond.Message {
			return false
		}
		if applyCond.ObservedGeneration != nil && statusCond.ObservedGeneration != 0 && *applyCond.ObservedGeneration != statusCond.ObservedGeneration {
			return false
		}
	}

	return true
}

func ApplyConfigForStatus(status sessiongatev1alpha1.SessionStatus) *sessiongatv1alpha1applyconfigurations.SessionStatusApplyConfiguration {
	cfg := sessiongatv1alpha1applyconfigurations.SessionStatus()

	if status.ExpiresAt != nil {
		cfg.WithExpiresAt(*status.ExpiresAt)
	}
	if status.Endpoint != "" {
		cfg.WithEndpoint(status.Endpoint)
	}
	if status.AuthorizationPolicyRef != "" {
		cfg.WithAuthorizationPolicyRef(status.AuthorizationPolicyRef)
	}
	if status.CredentialsSecretRef != "" {
		cfg.WithCredentialsSecretRef(status.CredentialsSecretRef)
	}
	if status.BackendKASURL != "" {
		cfg.WithBackendKASURL(status.BackendKASURL)
	}
	conditions := make([]*applyv1.ConditionApplyConfiguration, 0, len(status.Conditions))
	if status.Conditions != nil {
		for _, c := range status.Conditions {
			conditions = append(conditions, &applyv1.ConditionApplyConfiguration{
				Type:               &c.Type,
				Status:             &c.Status,
				Reason:             &c.Reason,
				Message:            &c.Message,
				ObservedGeneration: &c.ObservedGeneration,
				LastTransitionTime: &c.LastTransitionTime,
			})
		}
	}
	cfg.WithConditions(conditions...)

	return cfg
}

func NotReadyCondition(generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(string(ConditionTypeReady)).
		WithStatus(metav1.ConditionFalse).
		WithReason("NotReady").
		WithMessage("Session is not ready").
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func CredentialsNotAvailableCondition(reason, message string, generation int64, now time.Time) *applyv1.ConditionApplyConfiguration {
	return applyv1.Condition().
		WithType(string(ConditionTypeCredentialsAvailable)).
		WithStatus(metav1.ConditionFalse).
		WithReason(reason).
		WithMessage(message).
		WithObservedGeneration(generation).
		WithLastTransitionTime(metav1.NewTime(now))
}

func IsReady(status sessiongatev1alpha1.SessionStatus) bool {
	for _, condition := range status.Conditions {
		if condition.Type == string(ConditionTypeReady) && condition.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}
