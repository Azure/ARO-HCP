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

package capture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"syscall"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// CloseWithLog closes a helper resource without changing the probe outcome.
// Only a fixed resource label and an unwrapped errno go to stderr, never paths,
// protocol errors or certificate data. Stdout remains exclusively result JSON.
func CloseWithLog(resource string, close func() error) {
	if err := close(); err != nil {
		var errno syscall.Errno
		errors.As(err, &errno)
		log.New(os.Stderr, "", 0).Printf(`{"cleanup":%q,"errno":%d}`, resource, errno)
	}
}

// LogCleanupErrors forwards only recognized cleanup diagnostics from bounded
// helper stderr to the caller's logger, even when the helper otherwise succeeds.
// Arbitrary stderr (including panic output and command errors) is never logged.
func LogCleanupErrors(ctx context.Context, stderr []byte) {
	for line := range bytes.SplitSeq(stderr, []byte{'\n'}) {
		var entry struct {
			Resource string        `json:"cleanup"`
			Errno    syscall.Errno `json:"errno"`
		}
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		switch entry.Resource {
		case "namespace", "namespace root", "namespace directory", "verified namespace directory",
			"probe socket file", "probe socket", "DNS socket", "HTTP body", "evidence file", "route socket":
		default:
			continue
		}
		err := errors.New("cleanup failed")
		if entry.Errno != 0 {
			err = entry.Errno
		}
		utils.LoggerFromContext(ctx).Error(err, "Helper cleanup failed", "resource", entry.Resource)
	}
}
