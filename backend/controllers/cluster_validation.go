package controllers

import (
	"context"

	"github.com/Azure/ARO-HCP/internal/api"
)

// ClusterValidation represents a validation that can be performed on a cluster.
type ClusterValidation interface {
	// Name returns the name of the validation.
	Name() string
	// Validate validates the cluster.
	Validate(ctx context.Context, cluster *api.HCPOpenShiftCluster) error
}
