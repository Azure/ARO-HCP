package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Azure/ARO-HCP/pkg/api/arm"
)

// See https://eng.ms/docs/products/arm/api_contracts/resourcesystemdata
const systemDataHeaderName = "X-Ms-Arm-Resource-System-Data"

func MiddlewareSystemData(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	data := r.Header.Get(systemDataHeaderName)
	if data != "" {
		var systemData arm.SystemData
		err := json.Unmarshal([]byte(data), &systemData)
		if err != nil {
			// FIXME Log the error.
		}
		r = r.WithContext(context.WithValue(r.Context(), ContextKeySystemData, systemData))
	}

	next(w, r)
}
