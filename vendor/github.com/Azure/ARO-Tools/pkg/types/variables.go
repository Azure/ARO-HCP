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

package types

import "fmt"

// Variable
// Use this to pass in values to shell steps. Pairs a value with the environment variable name.
type Variable struct {
	Name  string `json:"name,omitempty"`
	Value `json:",inline"`
}

func (v *Variable) String() string {
	return fmt.Sprintf("$%s=%s", v.Name, v.Value.String())
}

// Value
// Use this to pass in values to pipeline steps. Values can come from various sources:
//   - Value: Use the value field to "hardcode" a value.
//   - ConfigRef: Use this to reference an entry in a config.Configuration.
//   - Input: Use this to specify an output chaining input.
type Value struct {
	Value     any    `json:"value,omitempty"`
	ConfigRef string `json:"configRef,omitempty"`
	Input     *Input `json:"input,omitempty"`
}

func (v *Value) String() string {
	if v.Value != nil {
		return fmt.Sprintf("%v", v.Value)
	}
	if v.ConfigRef != "" {
		return fmt.Sprintf("{{ %v }}", v.ConfigRef)
	}
	if v.Input != nil {
		return fmt.Sprintf("{{ inputs %s.%v }}", v.Input.Name, v.Input.Step)
	}
	return "unknown"
}

// Input
// Holds the values used for output chaining:
//   - Step: Referenced step
type Input struct {
	Name string `json:"name"`
	Step string `json:"step"`
}
