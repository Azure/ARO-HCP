package arm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

const (
	// Azure-specific HTTP header names
	HeaderNameAsyncOperation       = "Azure-AsyncOperation"
	HeaderNameAsyncNotification    = "Azure-AsyncNotification"
	HeaderNameAsyncNotificationURI = "Azure-AsyncNotificationUri"

	// Microsoft-specific HTTP header names
	HeaderNameErrorCode             = "X-Ms-Error-Code"
	HeaderNameHomeTenantID          = "X-Ms-Home-Tenant-Id"
	HeaderNameClientObjectID        = "X-Ms-Client-Object-Id"
	HeaderNameRequestID             = "X-Ms-Request-Id"
	HeaderNameClientRequestID       = "X-Ms-Client-Request-Id"
	HeaderNameCorrelationRequestID  = "X-Ms-Correlation-Request-Id"
	HeaderNameReturnClientRequestID = "X-Ms-Return-Client-Request-Id"
	HeaderNameARMResourceSystemData = "X-Ms-Arm-Resource-System-Data"
	HeaderNameIdentityURL           = "X-Ms-Identity-Url"
)
