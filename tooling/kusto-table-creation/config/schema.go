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

package config

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed tables.schema.json
var schemaJSON string

func loadSchema() (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(schemaJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal embedded schema: %v", err)
	}
	if err := c.AddResource("tables.schema.json", doc); err != nil {
		return nil, fmt.Errorf("failed to add schema resource: %v", err)
	}
	sch, err := c.Compile("tables.schema.json")
	if err != nil {
		return nil, fmt.Errorf("failed to compile embedded schema: %v", err)
	}
	return sch, nil
}
