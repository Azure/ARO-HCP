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
	"context"
	"fmt"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// contextKey is an unexported type used to embed content in the request context, so users must acquire the value with our getters.
type contextKey string

type ContextError struct {
	got any
	key contextKey
}

func (c *ContextError) Error() string {
	return fmt.Sprintf(
		"error retrieving value for key %q from context, value obtained was '%v' and type obtained was '%T'",
		c.key,
		c.got,
		c.got,
	)
}

const (
	contextKeyOriginalUrlPathValue = contextKey("url_path.original_value")
	contextKeyUrlPathValue         = contextKey("url_path.value")
	contextKeyHCPResourceID        = contextKey("hcp_resource_id")
	contextKeyClientPrincipalRef   = contextKey("client_principal_reference")
)

func ContextWithOriginalUrlPathValue(ctx context.Context, originalUrlPathValue string) context.Context {
	return context.WithValue(ctx, contextKeyOriginalUrlPathValue, originalUrlPathValue)
}

func OriginalUrlPathValueFromContext(ctx context.Context) (string, error) {
	originalUrlPathValue, ok := ctx.Value(contextKeyOriginalUrlPathValue).(string)
	if !ok {
		err := &ContextError{
			got: originalUrlPathValue,
			key: contextKeyOriginalUrlPathValue,
		}
		return originalUrlPathValue, err
	}
	return originalUrlPathValue, nil
}

func ContextWithUrlPathValue(ctx context.Context, urlPathValue string) context.Context {
	return context.WithValue(ctx, contextKeyUrlPathValue, urlPathValue)
}

func UrlPathValueFromContext(ctx context.Context) (string, error) {
	urlPathValue, ok := ctx.Value(contextKeyUrlPathValue).(string)
	if !ok {
		err := &ContextError{
			got: urlPathValue,
			key: contextKeyUrlPathValue,
		}
		return urlPathValue, err
	}
	return urlPathValue, nil
}

type PrincipalType string

const (
	PrincipalTypeDSTSUser            PrincipalType = "dstsUser"
	PrincipalTypeAADServicePrincipal PrincipalType = "aadServicePrincipal"
)

type ClientPrincipalReference struct {
	Name string
	Type PrincipalType
}

func ContextWithClientPrincipal(ctx context.Context, clientPrincipalReference ClientPrincipalReference) context.Context {
	ctx = utils.ContextWithLogger(ctx, utils.LoggerFromContext(ctx).WithValues("clientPrincipalName", clientPrincipalReference.Name))
	return context.WithValue(ctx, contextKeyClientPrincipalRef, clientPrincipalReference)
}

func ClientPrincipalFromContext(ctx context.Context) (ClientPrincipalReference, error) {
	clientPrincipalReference, ok := ctx.Value(contextKeyClientPrincipalRef).(ClientPrincipalReference)
	if !ok {
		return ClientPrincipalReference{}, fmt.Errorf("client principal reference not found in context")
	}
	return clientPrincipalReference, nil
}
