package v20240610preview

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"github.com/Azure/ARO-HCP/internal/api"
)

// APIVersion contains a version string as it will be used by clients
const APIVersion = "2024-06-10-preview"

type version struct{}

func init() {
	api.APIs[APIVersion] = &version{}
}
