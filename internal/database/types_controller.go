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
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

type Controller struct {
	TypedDocument `json:",inline"`

	ControllerProperties ControllerProperties `json:"properties"`
}

type ControllerProperties struct {
	// in order to migrate from old storage to new storage, we need to be able to read both the case when this is serialized
	// inline. the inline serialization is byte the new GenericDocument[api.Controller].
	// The order of the migration looks like:
	// 1. read both old and new. During this time, we must inject the resourceID into the items read from the database.
	//    a. write both old and new schemas.  This seems a little strange, but new backends can actually write old and new at the same time
	//       if we rollback, then we're able to read the old.  If we roll-forward again, we have the old data supercede the
	//       new data if it is present.
	// 2. deploy through to prod
	// 3. do a read/write cycle on all existing controllers.  Since we only have one, this actually happens every minute
	// 4. stop reading and writing old.
	//    a. we can do this in one step because the backend from #1 can read the new and use it, so rollback works fine.
	api.Controller `json:",inline"`
}

func (o *Controller) GetTypedDocument() *TypedDocument {
	return &o.TypedDocument
}

func (o *Controller) SetResourceID(_ *azcorearm.ResourceID) {
	// do nothing.  There is no real resource ID to set and we don't need to worry about conforming to ARM casing rules.
	// TODO, consider whether this should be done in the frontend and not in storage (likely)
}
