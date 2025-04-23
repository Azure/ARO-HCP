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

package frontend

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"testing"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func MiddlewarePanic(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	// Do not catch panics when running "go test".
	if !testing.Testing() {
		defer func() {
			if e := recover(); e != nil {
				logger := LoggerFromContext(r.Context())
				logger.Error(fmt.Sprintf("panic: %#v\n%s\n", e, string(debug.Stack())))
				arm.WriteInternalServerError(w)
			}
		}()
	}

	next(w, r)
}
