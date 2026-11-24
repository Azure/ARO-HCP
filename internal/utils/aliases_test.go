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

package utils

import (
	"context"
	"errors"
	"strings"
	"testing"

	cosmosclientutils "github.com/Azure/ARO-HCP/arohcp-cosmos-client/utils"
)

func TestTrackErrorAliasRecordsCallerLocation(t *testing.T) {
	err := TrackError(errors.New("boom"))
	if !strings.Contains(err.Error(), "(wrapped at aliases_test.go:") {
		t.Fatalf("TrackError recorded the wrong location; it must be re-exported as a variable, not wrapped: %q", err.Error())
	}
}

// Values stored through this package must be visible to the Cosmos client
// module, which reads them through its own package. Copying the helpers
// instead of aliasing them would give each package its own context keys.
func TestContextAliasesShareKeysWithCosmosClient(t *testing.T) {
	ctx := ContextWithControllerName(context.Background(), "test-controller")
	ctx = ContextWithInformerName(ctx, "test-informer")
	if name, ok := cosmosclientutils.ControllerNameFromContext(ctx); !ok || name != "test-controller" {
		t.Errorf("controller name not visible to arohcp-cosmos-client/utils: %q, %v", name, ok)
	}
	if name, ok := cosmosclientutils.InformerNameFromContext(ctx); !ok || name != "test-informer" {
		t.Errorf("informer name not visible to arohcp-cosmos-client/utils: %q, %v", name, ok)
	}
}
