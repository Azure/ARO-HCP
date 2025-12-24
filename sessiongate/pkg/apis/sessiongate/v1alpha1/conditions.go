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

package v1alpha1

import (
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ReasonUnknown                    = "Unknown"
	ReasonExpired                    = "Expired"
	ReasonAuthorizationFailed        = "AuthorizationPolicyFailed"
	ReasonConfiguringAuthorization   = "ConfiguringAuthorization"
	ReasonCredentialMintingFailed    = "CredentialMintingFailed"
	ReasonMintingCredentials         = "MintingCredentials"
	ReasonCertificatePending         = "CertificatePending"
	ReasonCredentialsFailed          = "CredentialsFailed"
	ReasonPrivateKeyCreated          = "PrivateKeyCreated"
	ReasonHostedControlPlaneNotFound = "HostedControlPlaneNotFound"
	ReasonAvailable                  = "Available"
	ReasonAsExpected                 = "AsExpected"
)

type ConditionType string

type ConditionSet struct {
	ready       ConditionType
	progressing ConditionType
	dependants  []ConditionType
}

// ConditionManager allows a resource to operate on its Conditions using higher
// order operations.
type ConditionManager interface {

	// GetReadyCondition finds and returns the ready Condition.
	GetReadyCondition() *metav1.Condition

	// MarkTrue marks the condition as true.
	MarkTrue(t ConditionType, reason string, message string)

	// MarkFalse marks the condition as false.
	MarkFalse(t ConditionType, reason string, message string)

	// InitializeConditions updates all Conditions in the ConditionSet to Unknown
	// if not set.
	InitializeConditions()
}

// conditionsImpl implements the helper methods for evaluating Conditions.
// +k8s:deepcopy-gen=false
type conditionsImpl struct {
	session *Session
	ConditionSet
}

// Manage creates a ConditionManager from an accessor object using the original
// ConditionSet as a reference. Status must be a pointer to a struct.
func (r ConditionSet) Manage(session *Session) ConditionManager {
	return conditionsImpl{
		session:      session,
		ConditionSet: r,
	}
}

// InitializeConditions updates all Conditions in the ConditionSet to Unknown
// if not set.
func (r conditionsImpl) InitializeConditions() {
	ready := r.GetCondition(r.ready)
	if ready == nil {
		r.setCondition(metav1.Condition{
			Type:   string(r.ready),
			Status: metav1.ConditionUnknown,
			Reason: ReasonUnknown,
		})
	}
	progressing := r.GetCondition(r.progressing)
	if progressing == nil {
		r.setCondition(metav1.Condition{
			Type:   string(r.progressing),
			Status: metav1.ConditionUnknown,
			Reason: ReasonUnknown,
		})
	}
	for _, t := range r.dependants {
		if c := r.GetCondition(t); c == nil {
			r.setCondition(metav1.Condition{
				Type:   string(t),
				Status: metav1.ConditionUnknown,
				Reason: ReasonUnknown,
			})
		}
	}
}

func (c conditionsImpl) GetReadyCondition() *metav1.Condition {
	return c.GetCondition(c.ready)
}

func (r conditionsImpl) GetCondition(t ConditionType) *metav1.Condition {
	if r.session == nil {
		return nil
	}

	for _, c := range r.session.Status.Conditions {
		if c.Type == string(t) {
			return &c
		}
	}
	return nil
}

// setCondition sets or updates the Condition on Conditions for the given ConditionType.
// If there is an update, Conditions are stored back sorted.
func (r conditionsImpl) setCondition(cond metav1.Condition) {
	if r.session == nil {
		return
	}
	if cond.Reason == "" {
		cond.Reason = ReasonUnknown
	}
	t := cond.Type
	var conditions []metav1.Condition
	for _, c := range r.session.Status.Conditions {
		if c.Type != t {
			conditions = append(conditions, c)
		} else {
			if cond.Status == c.Status && cond.Reason == c.Reason && cond.Message == c.Message && cond.ObservedGeneration == c.ObservedGeneration {
				return
			}
		}
	}
	cond.LastTransitionTime = metav1.NewTime(time.Now())
	conditions = append(conditions, cond)
	sort.Slice(conditions, func(i, j int) bool { return conditions[i].Type < conditions[j].Type })
	r.session.Status.Conditions = conditions
}

func (r conditionsImpl) MarkTrue(t ConditionType, reason string, message string) {
	r.setCondition(metav1.Condition{
		Type:               string(t),
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: r.session.Generation,
	})
	r.recomputeReady(t)
}

func (r conditionsImpl) MarkFalse(t ConditionType, reason string, message string) {
	r.setCondition(metav1.Condition{
		Type:               string(t),
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: r.session.Generation,
	})
	r.recomputeReady(t)
}

// recomputeReady marks the ready condition to true if all dependents are also true.
func (r conditionsImpl) recomputeReady(t ConditionType) {
	if c := r.findUnhappyDependent(); c != nil {
		// Propagate unhappy dependent to ready condition.
		r.setCondition(metav1.Condition{
			Type:               string(r.ready),
			Status:             c.Status,
			Reason:             c.Reason,
			Message:            c.Message,
			ObservedGeneration: r.session.Generation,
		})
	} else if t != r.ready {
		// Set the happy condition to true.
		r.setCondition(metav1.Condition{
			Type:               string(r.ready),
			Status:             metav1.ConditionTrue,
			Reason:             string(r.ready),
			Message:            "Session is ready",
			ObservedGeneration: r.session.Generation,
		})
	}
}

func (r conditionsImpl) findUnhappyDependent() *metav1.Condition {
	// This only works if there are dependents.
	if len(r.dependants) == 0 {
		return nil
	}

	// Do not modify the accessors condition order.
	unhappyConditions := []metav1.Condition{}
	for _, dependant := range r.dependants {
		if c := r.GetCondition(dependant); c != nil && c.Status != metav1.ConditionTrue {
			unhappyConditions = append(unhappyConditions, *c)
		}
	}

	// Sort set conditions by time.
	sort.Slice(unhappyConditions, func(i, j int) bool {
		return unhappyConditions[i].LastTransitionTime.After(unhappyConditions[j].LastTransitionTime.Time)
	})

	// First check the conditions with Status == False.
	for _, c := range unhappyConditions {
		if c.Status == metav1.ConditionFalse {
			return &c
		}
	}
	// Second check for conditions with Status == Unknown.
	for _, c := range unhappyConditions {
		if c.Status == metav1.ConditionUnknown {
			return &c
		}
	}

	// No unhappy dependents.
	return nil
}

const (
	ConditionTypeReady                        ConditionType = "Ready"
	ConditionTypeSessionActive                ConditionType = "SessionActive"
	ConditionTypeProgressing                  ConditionType = "Progressing"
	ConditionTypeCredentialsAvailable         ConditionType = "CredentialsAvailable"
	ConditionTypeAuthorizationPolicyAvailable ConditionType = "AuthorizationPolicyAvailable"
	ConditionTypeNetworkPathAvailable         ConditionType = "NetworkPathAvailable"
)

var sessionConditionSet = ConditionSet{
	ready:       ConditionTypeReady,
	progressing: ConditionTypeProgressing,
	dependants:  []ConditionType{ConditionTypeSessionActive, ConditionTypeCredentialsAvailable, ConditionTypeAuthorizationPolicyAvailable, ConditionTypeNetworkPathAvailable},
}
