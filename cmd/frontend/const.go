package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

type contextKey int

const (
	// Keys for request-scoped data in http.Request contexts
	ContextKeyOriginalPath contextKey = iota
	ContextKeyBody
	ContextKeyLogger
	ContextKeySystemData
)
