package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import "time"

// HCPOpenShiftClusterAdminCredential represents a temporary admin
// credential for an ARO HCP OpenShift cluster.
type HCPOpenShiftClusterAdminCredential struct {
	ExpirationTimestamp time.Time `json:"expirationTimestamp"`
	Kubeconfig          string    `json:"kubeconfig"`
}
