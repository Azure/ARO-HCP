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

package middleware

import (
	"net/http"
	"strings"
)

// WithLowercaseURLPathValue lowercases the URL path and adds the original and lowercase URL path values to the context.
func WithLowercaseURLPathValue(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := ContextWithOriginalUrlPathValue(
			r.Context(),
			r.URL.Path,
		)
		r.URL.Path = strings.ToLower(r.URL.Path)
		ctx = ContextWithUrlPathValue(
			ctx,
			r.URL.Path,
		)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
