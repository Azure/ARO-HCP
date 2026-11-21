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

package subscriptions

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestRegistrationConfigAwaitsEveryProvider(t *testing.T) {
	t.Parallel()
	encoded, err := registrationConfig(ParseProviderNamespaces(" Microsoft.Compute,,Microsoft.Network , Microsoft.Compute"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]struct{ Poll bool }
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 2 || !decoded["Microsoft.Compute"].Poll || !decoded["Microsoft.Network"].Poll {
		t.Fatalf("every provider must be awaited, got %s", encoded)
	}
	if _, err := registrationConfig(nil); err == nil {
		t.Fatal("an empty provider list must be rejected")
	}
}

func TestParseProviderNamespaces(t *testing.T) {
	t.Parallel()
	got := ParseProviderNamespaces(" Microsoft.Compute,,Microsoft.Network , Microsoft.Compute,")
	if !slices.Equal(got, []string{"Microsoft.Compute", "Microsoft.Network"}) {
		t.Fatalf("got %v", got)
	}
}
