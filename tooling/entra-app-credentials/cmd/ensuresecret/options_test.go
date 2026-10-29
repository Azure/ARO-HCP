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

package ensuresecret

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validOptions() *RawOptions {
	return &RawOptions{
		VaultURL: "https://kv.vault.azure.net",
		Mappings: []string{"My App;my-app-client-secret"},
		Timeout:  time.Minute,
	}
}

func TestValidateAcceptsMapping(t *testing.T) {
	validated, err := validOptions().Validate()

	require.NoError(t, err)
	require.Len(t, validated.bindings, 1)
	assert.Equal(t, "My App", validated.bindings[0].ApplicationDisplayName)
	assert.Equal(t, "my-app", validated.bindings[0].Prefix())
}

func TestValidateRejectsInvalidInput(t *testing.T) {
	for name, mutate := range map[string]func(*RawOptions){
		"secret without suffix": func(o *RawOptions) { o.Mappings = []string{"app;my-secret"} },
		"empty prefix":          func(o *RawOptions) { o.Mappings = []string{"app;-client-secret"} },
		"missing secret":        func(o *RawOptions) { o.Mappings = []string{"app"} },
		"duplicate application": func(o *RawOptions) { o.Mappings = []string{"app;a-client-secret", "app;b-client-secret"} },
		"duplicate secret":      func(o *RawOptions) { o.Mappings = []string{"a;x-client-secret", "b;x-client-secret"} },
		"no mapping":            func(o *RawOptions) { o.Mappings = nil },
		"vault with path":       func(o *RawOptions) { o.VaultURL = "https://kv.vault.azure.net/secrets" },
		"http vault":            func(o *RawOptions) { o.VaultURL = "http://kv.vault.azure.net" },
	} {
		t.Run(name, func(t *testing.T) {
			opts := validOptions()
			mutate(opts)
			_, err := opts.Validate()
			assert.Error(t, err)
		})
	}
}
