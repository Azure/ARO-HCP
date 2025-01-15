package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func MiddlewareSystemData(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := r.Context()
	logger := LoggerFromContext(ctx)

	// See https://eng.ms/docs/products/arm/api_contracts/resourcesystemdata
	data := r.Header.Get(arm.HeaderNameARMResourceSystemData)
	if data != "" {
		var systemData arm.SystemData
		err := json.Unmarshal([]byte(data), &systemData)
		if err == nil {
			ctx = ContextWithSystemData(ctx, &systemData)
			r = r.WithContext(ctx)
		} else {
			logger.Warn(fmt.Sprintf("Failed to parse %s header: %v", arm.HeaderNameARMResourceSystemData, err))
		}
	}

	next(w, r)
}
