package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

type contextKey int

const (
	// APIVersionKey is the request parameter name for the API version.
	APIVersionKey = "api-version"

	// Keys for request-scoped data in http.Request contexts
	ContextKeyOriginalPath contextKey = iota
	ContextKeyBody
	ContextKeyLogger
	ContextKeySystemData
)
