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

package logging

import (
	"log/slog"
	"os"
)

// New creates a new logger with the given verbosity level.
// Verbosity follows the convention: 0 = Info, positive values increase verbosity.
// The level is converted to slog.Level by negating (verbosity * 1) to match the original behavior.
func New(verbosity int) *slog.Logger {
	handlerOptions := &slog.HandlerOptions{
		Level: slog.Level(verbosity * -1),
	}
	handler := slog.NewJSONHandler(os.Stdout, handlerOptions)
	return slog.New(handler)
}
