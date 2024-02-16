package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"

	"github.com/Azure/ARO-HCP/pkg/api/arm"
)

func MiddlewarePanic(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	defer func() {
		if e := recover(); e != nil {
			arm.WriteError(
				w, http.StatusInternalServerError,
				arm.CloudErrorCodeInternalServerError, "",
				"Internal server error.")
		}
	}()

	next(w, r)
}
