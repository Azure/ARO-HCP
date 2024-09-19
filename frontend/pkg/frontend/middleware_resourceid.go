package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"net/http"

	"github.com/Azure/ARO-HCP/frontend/pkg/config"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// This middleware only applies to endpoints whose path form a valid Azure
// resource ID. It should follow the MiddlewareLowercase function.

func MiddlewareResourceID(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	logger, err := LoggerFromContext(r.Context())
	if err != nil {
		config.DefaultLogger().Error(err.Error())
		arm.WriteInternalServerError(w)
		return
	}

	originalPath, _ := OriginalPathFromContext(r.Context())
	if originalPath == "" {
		// MiddlewareLowercase has not run; fall back to the request path.
		logger.Warn("Middleware dependency error: MiddlewareResourceID ran before MiddlewareLowercase")
		originalPath = r.URL.Path
	}

	resourceID, err := arm.ParseResourceID(originalPath)
	if err == nil {
		ctx := ContextWithResourceID(r.Context(), resourceID)
		r = r.WithContext(ctx)
	} else {
		logger.Warn(fmt.Sprintf("Failed to parse '%s' as resource ID: %v", originalPath, err))
	}

	next(w, r)
}
