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

package assets

import (
	"fmt"
	"strings"

	"github.com/spf13/pflag"
)

// ApplyOptions sets handler options from entries of the form
// "<kind>.<key>=<value>". Each handler declares its own options through
// Handler.AttachOptions; keys are the names it declared. Later entries override
// earlier ones. An unknown kind, an unknown key, or a value the handler rejects
// is an error.
func (r *Registry) ApplyOptions(entries []string) error {
	for _, entry := range entries {
		name, value, found := strings.Cut(entry, "=")
		kindName, key, dotted := strings.Cut(name, ".")
		kindName, key = strings.TrimSpace(kindName), strings.TrimSpace(key)
		if !found || !dotted || kindName == "" || key == "" {
			return fmt.Errorf("%q must have the form <kind>.<key>=<value>", entry)
		}
		flags, registered := r.options[Kind(kindName)]
		if !registered {
			return fmt.Errorf("%q: unknown asset kind %q (registered: %s)", entry, kindName, r.kindList())
		}
		if flags.Lookup(key) == nil {
			if !flags.HasFlags() {
				return fmt.Errorf("%q: asset kind %q has no options", entry, kindName)
			}
			return fmt.Errorf("%q: asset kind %q has no option %q (valid: %s)", entry, kindName, key, optionNames(flags))
		}
		if err := flags.Set(key, strings.TrimSpace(value)); err != nil {
			return fmt.Errorf("%s.%s: %w", kindName, key, err)
		}
	}
	return nil
}

// OptionsUsage describes every option declared by the registered handlers,
// grouped by kind, for command help.
func (r *Registry) OptionsUsage() string {
	var usage strings.Builder
	for _, handler := range r.handlers {
		flags := r.options[handler.Kind()]
		if !flags.HasFlags() {
			continue
		}
		fmt.Fprintf(&usage, "%s options:\n", handler.Kind())
		flags.VisitAll(func(flag *pflag.Flag) {
			fmt.Fprintf(&usage, "  %s.%s=<%s> (default %s): %s\n", handler.Kind(), flag.Name, flag.Value.Type(), flag.DefValue, flag.Usage)
		})
	}
	return usage.String()
}

func (r *Registry) kindList() string {
	kinds := make([]string, 0, len(r.handlers))
	for _, handler := range r.handlers {
		kinds = append(kinds, string(handler.Kind()))
	}
	return strings.Join(kinds, ", ")
}

func optionNames(flags *pflag.FlagSet) string {
	var names []string
	flags.VisitAll(func(flag *pflag.Flag) { names = append(names, flag.Name) })
	return strings.Join(names, ", ")
}
