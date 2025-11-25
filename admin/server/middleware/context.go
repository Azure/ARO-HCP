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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
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

func ContextWithResourceID(ctx context.Context, resourceID *azcorearm.ResourceID) context.Context {
	return context.WithValue(ctx, contextKeyHCPResourceID, resourceID)
}

func ResourceIDFromContext(ctx context.Context) (*azcorearm.ResourceID, error) {
	resourceID, ok := ctx.Value(contextKeyHCPResourceID).(*azcorearm.ResourceID)
	if !ok {
		err := &ContextError{
			got: resourceID,
			key: contextKeyHCPResourceID,
		}
		return resourceID, err
	}
	return resourceID, nil
}
