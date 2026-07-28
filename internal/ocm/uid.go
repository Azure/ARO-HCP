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

package ocm

import (
	"encoding/base32"

	"github.com/segmentio/ksuid"
)

// NewUID generates a new unique identifier. These identifiers are generated with the `ksuid` which
// generates 20 bytes of random data, and then are encoded using a lower case variant of Base32,
// which results 32 lower case characters, which is still short and more friendly for names and
// labels of Kubernetes objects than the lower case and upper case mix generated directly by `ksuid`.
func NewUID() string {
	return uidEncoding.EncodeToString(ksuid.New().Bytes())
}

// uidAlphabet is the lower case alphabet used to encode unique identifiers.
const uidAlphabet = "0123456789abcdefghijklmnopqrstuv"

// uidEncoding is the lower case variant of Base32 used to encode unique identifiers.
var uidEncoding = base32.NewEncoding(uidAlphabet)
