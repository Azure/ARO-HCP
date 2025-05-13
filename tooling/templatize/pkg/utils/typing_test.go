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

package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAnyToString(t *testing.T) {
	assert.Equal(t, "foo", AnyToString("foo"))
	assert.Equal(t, "42", AnyToString(42))
	assert.Equal(t, "true", AnyToString(true))
	assert.Equal(t, "3.14", AnyToString(3.14))
}
