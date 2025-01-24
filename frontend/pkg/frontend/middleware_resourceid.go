package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"net/http"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// This middleware only applies to endpoints whose path form a valid Azure
// resource ID. It should follow the MiddlewareLowercase function.

func MiddlewareResourceID(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := r.Context()
	logger := LoggerFromContext(ctx)

	originalPath, _ := OriginalPathFromContext(ctx)
	if originalPath == "" {
		// MiddlewareLowercase has not run; fall back to the request path.
		logger.Warn("Middleware dependency error: MiddlewareResourceID ran before MiddlewareLowercase")
		originalPath = r.URL.Path
	}

	resourceID, err := azcorearm.ParseResourceID(originalPath)
	if err == nil {
		ctx = ContextWithResourceID(ctx, resourceID)
		r = r.WithContext(ctx)
	} else {
		logger.Warn(fmt.Sprintf("Failed to parse '%s' as resource ID: %v", originalPath, err))
	}

	next(w, r)
}
