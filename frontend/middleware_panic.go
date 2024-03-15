package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func MiddlewarePanic(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	defer func() {
		if e := recover(); e != nil {
			arm.WriteInternalServerError(w)
		}
	}()

	next(w, r)
}
