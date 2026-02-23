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

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type SessionConditionType string

const (
	SessionConditionTypeReady                       SessionConditionType = "Ready"
	SessionConditionTypeHostedControlPlaneAvailable SessionConditionType = "HostedControlPlaneAvailable"
	SessionConditionTypeCredentialsAvailable        SessionConditionType = "CredentialsAvailable"
	SessionConditionTypeNetworkPathAvailable        SessionConditionType = "NetworkPathAvailable"

	SessionReadyReason    string = "Ready"
	SessionNotReadyReason string = "NotReady"

	HostedControlPlaneNotFoundReason    string = "HostedControlPlaneNotFound"
	HostedControlPlaneAccessErrorReason string = "HostedControlPlaneAccessError"
	HostedControlPlaneNotReadyReason    string = "HostedControlPlaneNotReady"
	HostedControlPlaneAvailableReason   string = "HostedControlPlaneAvailable"

	CredentialsAvailableReason         string = "CredentialsAvailable"
	CredentialsSecretAccessErrorReason string = "CredentialsSecretAccessError"

	CertificateSigningRequestAccessErrorReason    string = "CertificateSigningRequestAccessError"
	CertificateSigningRequestPendingReason        string = "CertificateSigningRequestPending"
	CertificateSigningRequestCreationFailedReason string = "CertificateSigningRequestCreationFailed"

	PrivateKeyGenerationFailedReason string = "PrivateKeyGenerationFailed"

	NetworkPathAvailableReason string = "NetworkPathAvailable"
)

func (session *Session) IsReady() bool {
	readyCondition := session.GetCondition(SessionConditionTypeReady)
	return readyCondition != nil && readyCondition.Status == metav1.ConditionTrue
}

func (session *Session) GetCondition(conditionType SessionConditionType) *metav1.Condition {
	if session.Status.Conditions == nil {
		return nil
	}
	for _, condition := range session.Status.Conditions {
		if condition.Type == string(conditionType) {
			return &condition
		}
	}
	return nil
}
