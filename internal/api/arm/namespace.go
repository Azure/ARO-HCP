// Copyright 2026 Microsoft Corporation
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

package arm

import "k8s.io/apimachinery/pkg/util/sets"

type NamespaceOperation struct {
	// Operation name: {providerNamespace}/{resourceType}/{operation}
	Name string `json:"name"`

	// The object that describes the operation.
	Display NamespaceOperationDisplay `json:"display"`

	// Sources of requests to this operation. Valid values are
	// "user", "system", or, the default value, "user,system".
	Origin NamespaceOperationOrigin `json:"origin,omitempty"`

	// Indicates whether the operation applies to the data-plane.
	IsDataAction bool `json:"isDataAction"`
}

type NamespaceOperationDisplay struct {
	// The localized friendly form of the resource provider name.
	Provider string `json:"provider"`

	// The localized friendly form of the resource type related to this action/operation.
	Resource string `json:"resource"`

	// The localized friendly name for the operation, as it should be shown to the user.
	Operation string `json:"operation"`

	// The localized friendly description for the operation, as it should be shown to the user.
	Description string `json:"description"`
}

// The last path segment of Name must be one of these.
const (
	NamespaceOperationRead   = "read"
	NamespaceOperationWrite  = "write"
	NamespaceOperationDelete = "delete"
	NamespaceOperationAction = "action"
)

var (
	ValidNamespaceOperations = sets.New[string](
		NamespaceOperationRead,
		NamespaceOperationWrite,
		NamespaceOperationDelete,
		NamespaceOperationAction,
	)
)

type NamespaceOperationOrigin string

const (
	NamespaceOperationOriginUser       NamespaceOperationOrigin = "user"
	NamespaceOperationOriginSystem     NamespaceOperationOrigin = "system"
	NamespaceOperationOriginUserSystem NamespaceOperationOrigin = "user,system"
)

var (
	ValidNamespaceOperationOrigins = sets.New[NamespaceOperationOrigin](
		NamespaceOperationOriginUser,
		NamespaceOperationOriginSystem,
		NamespaceOperationOriginUserSystem,
	)
)
