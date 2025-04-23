// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package arm

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
