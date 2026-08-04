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

package listertestingutils

import (
	"context"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

// CollectFromIterator drains a cosmosstorageutils.DBClientIterator into a slice and propagates
// any iterator-level error.
func CollectFromIterator[T any](ctx context.Context, iter cosmosstorageutils.DBClientIterator[T]) ([]*T, error) {
	var out []*T
	for _, v := range iter.Items(ctx) {
		out = append(out, v)
	}
	if err := iter.GetError(); err != nil {
		return nil, err
	}
	return out, nil
}
