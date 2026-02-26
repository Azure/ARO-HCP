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
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	ClientPrincipalNameHeader = "X-Ms-Client-Principal-Name"
	ClientAADTypeHeader       = "X-Ms-Client-Principal-Type"
)

// MiddlewareClientPrincipal validates and extracts client principal information from request headers.
func MiddlewareClientPrincipal(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	logger := utils.LoggerFromContext(r.Context())

	var headers []string
	for name, values := range r.Header {
		if strings.HasPrefix(name, "X-Ms-Client-Principal-") {
			headers = append(headers, fmt.Sprintf("%s=%s", name, strings.Join(values, ",")))
		}
	}
	logger.Info("Geneva Action client principal headers", "headers", strings.Join(headers, "; "))

	clientPrincipalName := r.Header.Get(ClientPrincipalNameHeader)
	if clientPrincipalName == "" {
		http.Error(w, "client principal name not found", http.StatusUnauthorized)
		return
	}
	clientPrincipalType := r.Header.Get(ClientAADTypeHeader)
	if clientPrincipalType == "" {
		// once GA is rolled out to provide the type, we will make it mandatory
		// until then individual endpoints can decide if they demand it or not
		logger.Info("client principal type not found, continuing with empty type")
	}
	ctx := ContextWithClientPrincipal(r.Context(), ClientPrincipalReference{
		Name: clientPrincipalName,
		Type: PrincipalType(clientPrincipalType),
	})
	next(w, r.WithContext(ctx))
}
