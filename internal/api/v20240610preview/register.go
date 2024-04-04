package v20240610preview

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"github.com/Azure/ARO-HCP/internal/api"
)

type version struct{}

// String returns the api-version parameter value for this API.
func (v version) String() string {
	return "2024-06-10-preview"
}

var validate = api.NewValidator()

func init() {
	api.Register(version{})
}
