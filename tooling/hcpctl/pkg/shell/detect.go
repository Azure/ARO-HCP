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

package shell

import (
	"os"
	"os/exec"
	"runtime"
)

// detectShell detects the appropriate shell for the current operating system.
// This function unifies the shell detection logic that was previously
// duplicated between MC and HCP implementations.
//
// On Windows, it tries PowerShell Core first (cross-platform, modern),
// then Windows PowerShell (traditional), then falls back to Command Prompt.
//
// On Unix-like systems, it uses the SHELL environment variable if set,
// otherwise falls back to /bin/bash.
func detectShell() string {
	switch runtime.GOOS {
	case "windows":
		// Try PowerShell Core first (cross-platform, modern)
		if pwsh, err := exec.LookPath("pwsh.exe"); err == nil {
			return pwsh
		}
		// Try Windows PowerShell (traditional)
		if powershell, err := exec.LookPath("powershell.exe"); err == nil {
			return powershell
		}
		// Fallback to Command Prompt (always available on Windows)
		return "cmd.exe"
	default:
		// Unix-like systems (Linux, macOS, etc.)
		if shell := os.Getenv("SHELL"); shell != "" {
			return shell
		}
		return "/bin/bash"
	}
}
