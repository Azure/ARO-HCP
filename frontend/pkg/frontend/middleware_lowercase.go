package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"
	"strings"
)

func MiddlewareLowercase(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := ContextWithOriginalPath(r.Context(), r.URL.Path)
	r = r.WithContext(ctx)
	r.URL.Path = strings.ToLower(r.URL.Path)

	next(w, r)
}
