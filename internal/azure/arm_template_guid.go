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

package azure

import (
	"strings"

	"github.com/google/uuid"
)

// armTemplateGUIDNamespace is the UUID namespace used by the ARM template and Bicep guid() function.
var armTemplateGUIDNamespace = uuid.MustParse("11fb06fb-712d-4ddd-98c7-e71bbd588830")

// armTemplateGUID returns the same value as the ARM template and Bicep guid(parts...) function:
// a version 5 (SHA-1) UUID in the ARM namespace, computed over the parts joined with "-".
// The calculation is case-sensitive.
func armTemplateGUID(parts ...string) string {
	return uuid.NewSHA1(armTemplateGUIDNamespace, []byte(strings.Join(parts, "-"))).String()
}
