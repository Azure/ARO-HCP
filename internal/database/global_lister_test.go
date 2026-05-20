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

package database

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBillingGlobalListers verifies that BillingGlobalListers exposes a billing document global lister.
func TestBillingGlobalListers(t *testing.T) {
	gl := &cosmosBillingGlobalListers{
		billing: nil,
	}
	lister := gl.BillingDocs()
	require.NotNil(t, lister, "BillingDocs() should return a non-nil GlobalLister")
}
