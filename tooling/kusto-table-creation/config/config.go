package config

import (
	"fmt"
	"os"

	"github.com/stretchr/testify/assert/yaml"
	"k8s.io/utils/set"
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

// validateSchema validates raw YAML data against the embedded JSON Schema.
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

// ResolveColumns gets all columns from the table and its definitions.
func (c *Config) ResolveTableColumns(table Table) []Column {
	var allColumns []Column
	for _, defName := range table.Definitions {
		allColumns = append(allColumns, c.definitionMap[defName].Columns...)
	}
	allColumns = append(allColumns, table.Columns...)
	return allColumns
}

func validate(cfg *Config) error {
	defNames := set.New[string]()
	for _, d := range cfg.Definitions {
		if defNames.Has(d.Name) {
			return fmt.Errorf("duplicate definition name: %q", d.Name)
		}
		defNames.Insert(d.Name)
	}

	tableNames := set.New[string]()
	for _, t := range cfg.Tables {
		if tableNames.Has(t.Name) {
			return fmt.Errorf("duplicate table name: %q", t.Name)
		}
		tableNames.Insert(t.Name)

		for _, defRef := range t.Definitions {
			if !defNames.Has(defRef) {
				return fmt.Errorf("table %q references unknown definition: %q", t.Name, defRef)
			}
		}

		columnNamesSeen := set.New[string]()
		for _, defRef := range t.Definitions {
			for _, d := range cfg.Definitions {
				if d.Name == defRef {
					for _, col := range d.Columns {
						if columnNamesSeen.Has(col.Name) {
							return fmt.Errorf("table %q: duplicate column %q", t.Name, col.Name)
						}
						columnNamesSeen.Insert(col.Name)
					}
				}
			}
		}
		for _, col := range t.Columns {
			if columnNamesSeen.Has(col.Name) {
				return fmt.Errorf("table %q: duplicate column %q", t.Name, col.Name)
			}
			columnNamesSeen.Insert(col.Name)
		}
	}

	return nil
}
