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
	"context"
	"fmt"

	"github.com/go-logr/logr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

type ContextError struct {
	got any
	key contextKey
}

func (c *ContextError) Error() string {
	return fmt.Sprintf(
		"error retrieving value for key %q from context, value obtained was '%v' and type obtained was '%T'",
		c.key,
		c.got,
		c.got)
}

type contextKey int

func (c contextKey) String() string {
	switch c {
	case contextKeyResourceID:
		return "resourceID"
	}
	return "<unknown>"
}

const (
	// Keys for request-scoped data in http.Request contexts
	contextKeyResourceID contextKey = iota
)

func ContextWithLogger(ctx context.Context, logger logr.Logger) context.Context {
	return logr.NewContext(ctx, logger)
}

func LoggerFromContext(ctx context.Context) logr.Logger {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		// Return the default logger as a fail-safe, but log
		// the failure to obtain the logger from the context.
		logger = DefaultLogger()
		logger.Error(err, "failed to get logger from context")
	}
	return logger
}

func ContextWithResourceID(ctx context.Context, resourceID *azcorearm.ResourceID) context.Context {
	return context.WithValue(ctx, contextKeyResourceID, resourceID)
}

func ResourceIDFromContext(ctx context.Context) (*azcorearm.ResourceID, error) {
	resourceID, ok := ctx.Value(contextKeyResourceID).(*azcorearm.ResourceID)
	if !ok {
		err := &ContextError{
			got: resourceID,
			key: contextKeyResourceID,
		}
		return resourceID, err
	}
	return resourceID, nil
}
