package main


// This will invoke the init() function in each
// API version package so it can register itself.
import (
	_ "github.com/Azure/ARO-HCP/internal/api/v20240610preview"
)
