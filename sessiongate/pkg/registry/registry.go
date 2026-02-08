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

package registry

import (
	"k8s.io/client-go/rest"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
)

// SessionRegistry defines the interface for registering and unregistering sessions
// with a session server. This abstraction allows for easier testing by enabling
// mock implementations.
type SessionRegistry interface {
	// RegisterSession registers a session with the given options and returns
	// the public endpoint URL for accessing the session.
	RegisterSession(sessionName, resourceID string, owner sessiongatev1alpha1.Principal, restConfig *rest.Config) (string, error)

	// UnregisterSession removes a session registration by its session ID.
	UnregisterSession(sessionName string)

	// GetSessionEndpoint computes the public endpoint URL for a session ID
	// without registering it. This is useful for updating status before
	// registration completes.
	GetSessionEndpoint(sessionID string) string
}
