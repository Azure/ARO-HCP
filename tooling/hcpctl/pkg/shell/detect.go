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
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// detectShell detects the appropriate shell for the current operating system
// and returns a Shell implementation.
//
// On Windows, it tries PowerShell Core first, then Windows PowerShell.
// On Unix-like systems, it uses the SHELL environment variable if set,
// otherwise falls back to /bin/bash.
func detectShell() (Shell, error) {
	switch runtime.GOOS {
	case "windows":
		// try PowerShell Core first (modern)
		if pwsh, err := exec.LookPath("pwsh.exe"); err == nil {
			return NewPowerShell(pwsh), nil
		}
		// try Windows PowerShell (traditional)
		if powershell, err := exec.LookPath("powershell.exe"); err == nil {
			return NewPowerShell(powershell), nil
		}
		// no fallback to cmd.exe - PowerShell is required on Windows
		return nil, fmt.Errorf("PowerShell not found. Please install PowerShell Core (pwsh) or Windows PowerShell")
	default:
		// unix-like systems (Linux, macOS, etc.)
		if shell := os.Getenv("SHELL"); shell != "" {
			return NewUnixShell(shell), nil
		}
		return NewUnixShell("/bin/bash"), nil
	}
}
