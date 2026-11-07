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

package cmd

import (
	"fmt"
	"sort"
)

// stringMapFlag collects repeated key=value flags.
type stringMapFlag map[string]string

// String renders the flag values for help output.
func (m stringMapFlag) String() string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key+"="+m[key])
	}
	sort.Strings(keys)
	return fmt.Sprint(keys)
}

// Set parses one key=value pair.
func (m stringMapFlag) Set(value string) error {
	key, item, found := cut(value, "=")
	if !found || key == "" || item == "" {
		return fmt.Errorf("expected FAMILY=VERSION, got %q", value)
	}
	m[key] = item
	return nil
}

// Type reports the flag type.
func (m stringMapFlag) Type() string { return "family=version" }

// Splits a value at the first separator.
func cut(value, separator string) (string, string, bool) {
	for index := 0; index+len(separator) <= len(value); index++ {
		if value[index:index+len(separator)] == separator {
			return value[:index], value[index+len(separator):], true
		}
	}
	return value, "", false
}
