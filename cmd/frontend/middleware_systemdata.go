package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Azure/ARO-HCP/pkg/api/arm"
)

func MiddlewareSystemData(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	// See https://eng.ms/docs/products/arm/api_contracts/resourcesystemdata
	data := r.Header.Get(arm.HeaderNameARMResourceSystemData)
	if data != "" {
		var systemData arm.SystemData
		err := json.Unmarshal([]byte(data), &systemData)
		if err != nil {
			if logger, ok := r.Context().Value(ContextKeyLogger).(*slog.Logger); ok {
				logger.Warn(fmt.Sprintf("Failed to parse %s header: %w", arm.HeaderNameARMResourceSystemData, err))
			}
		}
		r = r.WithContext(context.WithValue(r.Context(), ContextKeySystemData, systemData))
	}

	next(w, r)
}
