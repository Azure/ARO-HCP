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

package maestro

import (
	"context"
	"fmt"
	"os"

	"github.com/go-logr/logr"

	ocmsdkgologging "github.com/openshift-online/ocm-sdk-go/logging"
)

// logrToOCMLoggerAdapter adapts a logr.Logger to the ocm-sdk-go logging.Logger interface
// so the logger from utils.LoggerFromContext can be passed to Maestro clients.
type logrToOCMLoggerAdapter struct {
	logr.Logger
}

// Ensure logrToOCMLogger implements logging.Logger at compile time.
var _ ocmsdkgologging.Logger = (*logrToOCMLoggerAdapter)(nil)

// LogrToOCMLogger returns an ocm-sdk-go Logger that delegates to the given logr.Logger.
func NewLogrToOCMLoggerAdapter(l logr.Logger) ocmsdkgologging.Logger {
	return &logrToOCMLoggerAdapter{Logger: l}
}

func (l *logrToOCMLoggerAdapter) DebugEnabled() bool { return l.V(1).Enabled() }
func (l *logrToOCMLoggerAdapter) InfoEnabled() bool  { return true }
func (l *logrToOCMLoggerAdapter) WarnEnabled() bool  { return true }
func (l *logrToOCMLoggerAdapter) ErrorEnabled() bool { return true }

func (l *logrToOCMLoggerAdapter) Debug(ctx context.Context, format string, args ...interface{}) {
	if l.DebugEnabled() {
		l.Logger.V(1).Info(fmt.Sprintf("[DEBUG] %s", fmt.Sprintf(format, args...)))
	}
}

func (l *logrToOCMLoggerAdapter) Info(ctx context.Context, format string, args ...interface{}) {
	l.Logger.Info(fmt.Sprintf(format, args...))
}

func (l *logrToOCMLoggerAdapter) Warn(ctx context.Context, format string, args ...interface{}) {
	l.Logger.Info(fmt.Sprintf("[WARN] %s", fmt.Sprintf(format, args...)))
}

func (l *logrToOCMLoggerAdapter) Error(ctx context.Context, format string, args ...interface{}) {
	l.Logger.Error(nil, fmt.Sprintf(format, args...))
}

func (l *logrToOCMLoggerAdapter) Fatal(ctx context.Context, format string, args ...interface{}) {
	l.Logger.Error(nil, fmt.Sprintf(format, args...))
	os.Exit(1)
}
