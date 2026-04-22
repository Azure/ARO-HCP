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

package config

import (
	"fmt"
	"os"

	"sigs.k8s.io/yaml"
)

// Column represents a single Kusto table column with its type and ingestion mapping.
type Column struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Mapping string `json:"mapping"`
}

// Definition is a reusable group of columns that can be referenced by tables.
type Definition struct {
	Name    string   `json:"name"`
	Columns []Column `json:"columns"`
}

// Table represents a Kusto table schema, composed from definitions and inline columns.
type Table struct {
	Name        string   `json:"name"`
	Databases   []string `json:"databases"`
	Definitions []string `json:"definitions,omitempty"`
	Columns     []Column `json:"columns,omitempty"`
}

// Config is the top-level YAML schema.
type Config struct {
	Definitions []Definition `json:"definitions"`
	Tables      []Table      `json:"tables"`

	definitionMap map[string]Definition
}

func validateSchema(data []byte) error {
	compiledSchema, err := loadSchema()
	if err != nil {
		return err
	}
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}
	return compiledSchema.Validate(raw)
}

// NewConfigFromFile reads, parses, and validates the YAML configuration file.
func NewConfigFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := validateSchema(data); err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if err := validate(&cfg); err != nil {
		return nil, err
	}
	cfg.definitionMap = make(map[string]Definition)
	for _, d := range cfg.Definitions {
		cfg.definitionMap[d.Name] = d
	}
	return &cfg, nil
}

// ResolveTableColumns gets all columns from the table and its definitions.
func (c *Config) ResolveTableColumns(table Table) []Column {
	var allColumns []Column
	for _, defName := range table.Definitions {
		allColumns = append(allColumns, c.definitionMap[defName].Columns...)
	}
	allColumns = append(allColumns, table.Columns...)
	return allColumns
}

func validate(cfg *Config) error {
	defNames := make(map[string]struct{})
	for _, d := range cfg.Definitions {
		if _, exists := defNames[d.Name]; exists {
			return fmt.Errorf("duplicate definition name: %q", d.Name)
		}
		defNames[d.Name] = struct{}{}
	}

	tableNames := make(map[string]struct{})
	for _, t := range cfg.Tables {
		if _, exists := tableNames[t.Name]; exists {
			return fmt.Errorf("duplicate table name: %q", t.Name)
		}
		tableNames[t.Name] = struct{}{}

		for _, defRef := range t.Definitions {
			if _, exists := defNames[defRef]; !exists {
				return fmt.Errorf("table %q references unknown definition: %q", t.Name, defRef)
			}
		}

		columnNamesSeen := make(map[string]struct{})
		for _, defRef := range t.Definitions {
			for _, d := range cfg.Definitions {
				if d.Name == defRef {
					for _, col := range d.Columns {
						if _, exists := columnNamesSeen[col.Name]; exists {
							return fmt.Errorf("table %q: duplicate column %q", t.Name, col.Name)
						}
						columnNamesSeen[col.Name] = struct{}{}
					}
				}
			}
		}
		for _, col := range t.Columns {
			if _, exists := columnNamesSeen[col.Name]; exists {
				return fmt.Errorf("table %q: duplicate column %q", t.Name, col.Name)
			}
			columnNamesSeen[col.Name] = struct{}{}
		}
	}

	return nil
}
