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

package kusto

import (
	"encoding/json"
	"fmt"
)

type MappingColumn struct {
	Column, DataType, Path string
	Properties             struct{ Path string }
}

func ParseMapping(mapping string) ([]MappingColumn, error) {
	var columns []MappingColumn
	if err := json.Unmarshal([]byte(mapping), &columns); err != nil {
		return nil, fmt.Errorf("decode mapping: %w", err)
	}
	return columns, nil
}

func (c MappingColumn) Matches(column, dataType string) bool {
	path := "$['" + column + "']"
	return c.Column == column && c.DataType == dataType && (c.Path == path || c.Properties.Path == path)
}
