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
)

const (

	// ControllerAgentName distinguishes this controller from other things writing to API objects
	ControllerAgentName = "sessiongate-controller"

	// LabelManagedBy identifies resources managed by the sessiongate controller
	LabelManagedBy = "app.kubernetes.io/managed-by"

	// AnnotationSessiongate identifies the namespace/session pair that the resource belongs to
	// this is used on resources where owner references are not possible (e.g. cross cluster resources)
	AnnotationSessiongate = "sessiongate.aro-hcp.azure.com/session"

	// RSAKeySize is the size in bits for RSA private keys generated for session credentials
	RSAKeySize = 2048

	// MCProviderCacheSyncTimeout is the maximum time to wait for management cluster
	// informer caches to sync during provider registration
	MCProviderCacheSyncTimeout = 30 * time.Second

	// MinCSRExpirationSeconds is the minimum CSR expiration allowed by Kubernetes (10 minutes).
	// See https://kubernetes.io/docs/reference/access-authn-authz/certificate-signing-requests/
	MinCSRExpirationSeconds = 600
)

// ManagedByLabelSelector returns a label selector string for resources managed by this controller
// This is used to filter informers to only watch resources created and managed by sessiongate-controller
func ManagedByLabelSelector() string {
	return fmt.Sprintf("%s=%s", LabelManagedBy, ControllerAgentName)
}
