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

// Package breakglass provides HTTP handlers for breakglass session management.
//
// # Security Model
//
// The breakglass package has two distinct security models depending on the endpoint:
//
// # Session Creation and Kubeconfig Endpoints (create.go, kubeconfig.go)
//
// These endpoints are accessed exclusively via Geneva Actions:
//
// Network Layer:
//   - Ingress is restricted to Geneva Actions service tags
//
// Authentication (MISE):
//   - MISE validates that the caller is the ARO HCP Geneva Actions service identity
//   - Client principal identity (the user who initiated the Geneva Action) is extracted from the X-Ms-Client-Principal-Name header
//
// Authorization (Geneva Actions):
// Authorization decisions happen before the admin API is called:
//   - Lockbox: Requires an approved access request
//   - Group membership: Caller must be in authorized security groups
//   - Oncall rotation: May require active oncall status
//
// The handlers trust the authenticated principal after passing through these layers.
// The principal identity is recorded in the Session CR for audit purposes and
// HCP KAS access control.
//
// # HCP Access Proxy Endpoint (proxy.go)
//
// This endpoint proxies authenticated requests to the HCP's kube-apiserver:
//
// Authentication (MISE):
//   - Accepts Azure access tokens (user or service principal)
//   - MISE validates the token and extracts the principal identity
//
// Authorization (Session Ownership):
//   - Access is granted based on breakglass session ownership
//   - No Geneva Actions involvement - direct access with valid Azure credentials
//
// # Session Lifecycle
//
// Sessions are temporary access grants with a defined TTL:
//
//   - Sessions are created with a TTL between 1 minute and 24 hours
//   - The sessiongate controller monitors session expiry
//   - Expired sessions are automatically deleted by the controller
package breakglass
