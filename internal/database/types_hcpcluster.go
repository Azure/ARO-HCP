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

package database

import (
	"github.com/Azure/ARO-HCP/internal/api"
)

type HCPCluster struct {
	TypedDocument `json:",inline"`

	HCPClusterProperties `json:"properties"`
}

type HCPClusterProperties struct {
	// HCPOpenShiftCluster is inlined directly. The on-disk shape now matches
	// GenericDocument[api.HCPOpenShiftCluster] and HCPCluster only exists as a
	// distinct type while the migration to that generic surface completes.
	api.HCPOpenShiftCluster `json:",inline"`
}

func (o *HCPCluster) GetTypedDocument() *TypedDocument {
	return &o.TypedDocument
}
