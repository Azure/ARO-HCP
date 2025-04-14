package mocks

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

//go:generate go tool mockgen -typed -source=../database/database.go -destination=dbclient.go -package mocks github.com/Azure/ARO-HCP/internal/database DBClient
//go:generate go tool mockgen -typed -source=../ocm/ocm.go -destination=ocm.go -package mocks github.com/Azure/ARO-HCP/internal/ocm ClusterServiceClientSpec
