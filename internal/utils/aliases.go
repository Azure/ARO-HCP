// Copyright 2026 Microsoft Corporation
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

package utils

import (
	cosmosclientutils "github.com/Azure/ARO-HCP/arohcp-cosmos-client/utils"
)

// The context, logging and error-tracking helpers live in
// arohcp-cosmos-client/utils so that module doesn't depend on internal/.
// These aliases keep existing callers of this package working.

type (
	ContextError      = cosmosclientutils.ContextError
	LineTrackingError = cosmosclientutils.LineTrackingError
	LogValues         = cosmosclientutils.LogValues
	LoggableKey       = cosmosclientutils.LoggableKey
)

var (
	DefaultLogger = cosmosclientutils.DefaultLogger

	ContextWithLogger         = cosmosclientutils.ContextWithLogger
	LoggerFromContext         = cosmosclientutils.LoggerFromContext
	ContextWithResourceID     = cosmosclientutils.ContextWithResourceID
	ResourceIDFromContext     = cosmosclientutils.ResourceIDFromContext
	ContextWithControllerName = cosmosclientutils.ContextWithControllerName
	ControllerNameFromContext = cosmosclientutils.ControllerNameFromContext
	ContextWithInformerName   = cosmosclientutils.ContextWithInformerName
	InformerNameFromContext   = cosmosclientutils.InformerNameFromContext
	AddLoggerValues           = cosmosclientutils.AddLoggerValues

	// TrackError must stay a variable rather than a wrapper function: it records
	// its caller's file and line with runtime.Caller(1), so a wrapper would
	// record the wrapper's location instead.
	TrackError                      = cosmosclientutils.TrackError
	ErrorMessageWithoutLineTracking = cosmosclientutils.ErrorMessageWithoutLineTracking
)
