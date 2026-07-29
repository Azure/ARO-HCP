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

package snapshot

import (
	"bytes"
	"strings"
)

// CleanConsoleLog processes raw VM serial console output to make it readable
// as plain text. It performs three transformations:
//
//  1. Strips VT100/ANSI terminal escape sequences (colors, cursor movement,
//     screen clearing, etc.) that are meaningless outside a terminal emulator.
//  2. Strips the GRUB boot menu preamble (everything before "Booting '..." or
//     "Booting `..."), which is rendered as framebuffer box-drawing characters
//     and cursor positioning that produce unreadable noise.
//  3. Removes blank lines (empty or whitespace-only), which are artifacts of
//     stripped escape sequences and interleaved systemd/dmesg output.
//
// The original unprocessed file remains available via the artifact URL in the
// manifest for anyone who needs the raw serial output.
func CleanConsoleLog(data []byte) []byte {
	stripped := stripANSIEscapes(data)
	stripped = normalizeLineEndings(stripped)
	stripped = stripGRUBPreamble(stripped)
	stripped = removeBlankLines(stripped)
	return stripped
}

// stripANSIEscapes removes ECMA-48 / VT100 terminal escape sequences using a
// byte-level state machine. This handles:
//   - CSI sequences: ESC [ <params> <intermediate> <final>
//   - OSC sequences: ESC ] ... (terminated by ST or BEL)
//   - Two-character sequences: ESC <char> (e.g. ESC ( B, ESC ) 0)
//   - Single-byte C1 controls in the 0x80-0x9F range (rare in practice)
//
// Non-escape control characters (newlines, tabs, carriage returns) are preserved.
func stripANSIEscapes(data []byte) []byte {
	out := make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		b := data[i]

		// ESC (0x1B) starts an escape sequence.
		if b == 0x1B {
			i++
			if i >= len(data) {
				break
			}
			switch data[i] {
			case '[':
				// CSI sequence: ESC [ <params;...> <final byte 0x40-0x7E>
				i++
				for i < len(data) && data[i] >= 0x20 && data[i] <= 0x3F {
					i++ // parameter bytes (0-9 ; < = > ?)
				}
				for i < len(data) && data[i] >= 0x20 && data[i] <= 0x2F {
					i++ // intermediate bytes (space ! " ... /)
				}
				if i < len(data) && data[i] >= 0x40 && data[i] <= 0x7E {
					i++ // final byte (@A-Z[\]^_`a-z{|}~)
				}
			case ']':
				// OSC sequence: ESC ] ... terminated by BEL (0x07) or ST (ESC \)
				i++
				for i < len(data) {
					if data[i] == 0x07 {
						i++
						break
					}
					if data[i] == 0x1B && i+1 < len(data) && data[i+1] == '\\' {
						i += 2
						break
					}
					i++
				}
			case '(', ')', '*', '+':
				// Designate character set: ESC ( <char>, ESC ) <char>, etc.
				i++
				if i < len(data) {
					i++ // consume the charset designator
				}
			default:
				// Two-character escape: ESC <char> (mode set/reset, etc.)
				if data[i] >= 0x20 && data[i] <= 0x7E {
					i++
				}
			}
			continue
		}

		// Strip C1 control characters (0x80-0x9F) that may appear as single-byte
		// equivalents of ESC-initiated sequences. These are rare in serial console
		// output but technically valid.
		if b >= 0x80 && b <= 0x9F {
			i++
			continue
		}

		out = append(out, b)
		i++
	}
	return out
}

// normalizeLineEndings converts \r\n to \n and strips bare \r characters.
// Serial console output uses \r\n line endings; bare \r may appear from
// cursor-return operations that produce empty-looking lines.
func normalizeLineEndings(data []byte) []byte {
	// First replace \r\n with \n, then strip remaining bare \r.
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	data = bytes.ReplaceAll(data, []byte("\r"), []byte(""))
	return data
}

// stripGRUBPreamble removes everything before the GRUB "Booting" message.
// The GRUB boot menu renders as box-drawing characters and cursor positioning
// escape sequences that produce unreadable noise after escape stripping. After
// the escape codes are removed, cursor-positioned text concatenates into one
// long line, so we cut at the marker position itself (not at line start).
// The "Booting '..." or "Booting `..." text marks the transition from GRUB to
// the kernel, which is where useful diagnostic content begins.
func stripGRUBPreamble(data []byte) []byte {
	// Look for "Booting '" or "Booting `" which marks the kernel handoff.
	markers := []string{"Booting '", "Booting `"}
	for _, marker := range markers {
		idx := bytes.Index(data, []byte(marker))
		if idx >= 0 {
			return data[idx:]
		}
	}
	// No GRUB preamble found — return as-is.
	return data
}

// removeBlankLines strips lines that are empty or contain only whitespace.
// This cleans up the visual gaps left by stripped escape sequences and the
// interleaved systemd/dmesg output.
func removeBlankLines(data []byte) []byte {
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines))

	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}

	return []byte(strings.Join(out, "\n"))
}
