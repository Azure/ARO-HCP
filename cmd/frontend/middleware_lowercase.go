package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"net/http"
	"strings"
)

func MiddlewareLowercase(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	r = r.WithContext(context.WithValue(r.Context(), ContextKeyOriginalPath, r.URL.Path))
	r.URL.Path = strings.ToLower(r.URL.Path)

	next(w, r)
}
