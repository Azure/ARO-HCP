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

package main

import (
	"errors"
	"sync"

	"github.com/onsi/gomega/format"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

var azureErrorFormatterOnce sync.Once

func configureGomegaFormatting() {
	// Retain complete failure messages and Gomega's default depth handling.
	format.MaxLength = 0
	azureErrorFormatterOnce.Do(func() {
		format.RegisterCustomFormatter(func(value any) (string, bool) {
			err, ok := value.(error)
			if !ok {
				return "", false
			}
			var responseErr *azcore.ResponseError
			if !errors.As(err, &responseErr) {
				return "", false
			}
			// Match the outer error, including wrappers and joins, before reflection
			// reaches private fields. Gomega already prints its Error() message.
			// Nested errors only get this marker, not a separate Error() message.
			return "<Azure error internals omitted>", true
		})
	})
}
