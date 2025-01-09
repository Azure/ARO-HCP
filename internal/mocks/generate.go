package mocks

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

//go:generate $MOCKGEN -source=../database/database.go -destination=dbclient.go -package mocks github.com/Azure/ARO-HCP/internal/database DBClient
