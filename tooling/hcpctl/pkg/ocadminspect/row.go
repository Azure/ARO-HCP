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

package ocadminspect

import (
	"encoding/json"

	"github.com/Azure/azure-kusto-go/azkustodata/types"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

// rowToMap converts a Kusto row into a column-name -> value map. Dynamic columns
// (e.g. the snapshot `object`) are JSON-decoded into nested Go structures; all
// other columns are rendered as strings.
func rowToMap(tagged kusto.TaggedRow) map[string]any {
	row := tagged.Row
	columns := row.Columns()
	values := row.Values()

	result := make(map[string]any, len(columns))
	for idx, col := range columns {
		if idx >= len(values) {
			break
		}
		value := values[idx]
		if value.GetType() == types.Dynamic {
			if raw, ok := value.GetValue().([]byte); ok {
				var parsed any
				if err := json.Unmarshal(raw, &parsed); err == nil {
					result[col.Name()] = parsed
					continue
				}
			}
		}
		result[col.Name()] = value.String()
	}
	return result
}
