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
	"strings"
	"testing"
)

func TestStripANSIEscapes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no escapes",
			input:    "hello world\n",
			expected: "hello world\n",
		},
		{
			name:     "CSI color codes",
			input:    "\x1b[0;32m  OK  \x1b[0m] Started \x1b[0;1;39mJournal Service\x1b[0m.",
			expected: "  OK  ] Started Journal Service.",
		},
		{
			name:     "CSI cursor positioning",
			input:    "\x1b[05;78H\x1b[23;01H   The highlighted entry\x1b[05;78H",
			expected: "   The highlighted entry",
		},
		{
			name:     "CSI screen clear",
			input:    "\x1b[2Jhello",
			expected: "hello",
		},
		{
			name:     "OSC sequence with BEL terminator",
			input:    "before\x1b]0;title\x07after",
			expected: "beforeafter",
		},
		{
			name:     "OSC sequence with ST terminator",
			input:    "before\x1b]0;title\x1b\\after",
			expected: "beforeafter",
		},
		{
			name:     "character set designation",
			input:    "before\x1b(Bafter",
			expected: "beforeafter",
		},
		{
			name:     "two-character escape",
			input:    "before\x1bcafter",
			expected: "beforeafter",
		},
		{
			name:     "preserves newlines and tabs",
			input:    "\x1b[32mhello\x1b[0m\n\tworld\n",
			expected: "hello\n\tworld\n",
		},
		{
			name:     "systemd OK line",
			input:    "[\x1b[0;32m  OK  \x1b[0m] Reached target \x1b[0;1;39mSwaps\x1b[0m.",
			expected: "[  OK  ] Reached target Swaps.",
		},
		{
			name:     "CSI with question mark param",
			input:    "\x1b[?25lhidden cursor\x1b[?25h",
			expected: "hidden cursor",
		},
		{
			name:     "truncated escape at end",
			input:    "hello\x1b",
			expected: "hello",
		},
		{
			name:     "multiple escapes in kernel line",
			input:    "[    1.567296] systemd[1]: systemd 252-51.el9_6.2 running in system mode",
			expected: "[    1.567296] systemd[1]: systemd 252-51.el9_6.2 running in system mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(stripANSIEscapes([]byte(tt.input)))
			if got != tt.expected {
				t.Errorf("stripANSIEscapes(%q):\n  got:  %q\n  want: %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestStripGRUBPreamble(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no GRUB preamble",
			input:    "[    0.000000] Linux version 5.14.0\nmore stuff\n",
			expected: "[    0.000000] Linux version 5.14.0\nmore stuff\n",
		},
		{
			name:     "with backtick GRUB preamble",
			input:    "GRUB version 2.06\nsome menu stuff\n  Booting `Red Hat Enterprise Linux CoreOS'\n[    0.000000] Linux\n",
			expected: "Booting `Red Hat Enterprise Linux CoreOS'\n[    0.000000] Linux\n",
		},
		{
			name:     "with single-quote GRUB preamble",
			input:    "menu\nBooting 'RHCOS'\nkernel\n",
			expected: "Booting 'RHCOS'\nkernel\n",
		},
		{
			name:     "booting marker mid-line (concatenated GRUB menu)",
			input:    "junk menu stuff *Red Hat EL CoreOS   Booting `OS'\nkernel\n",
			expected: "Booting `OS'\nkernel\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(stripGRUBPreamble([]byte(tt.input)))
			if got != tt.expected {
				t.Errorf("stripGRUBPreamble():\n  got:  %q\n  want: %q", got, tt.expected)
			}
		})
	}
}

func TestRemoveBlankLines(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no blank lines",
			input:    "a\nb\nc",
			expected: "a\nb\nc",
		},
		{
			name:     "single blank line removed",
			input:    "a\n\nb",
			expected: "a\nb",
		},
		{
			name:     "multiple blank lines removed",
			input:    "a\n\n\n\n\nb",
			expected: "a\nb",
		},
		{
			name:     "whitespace-only lines removed",
			input:    "a\n  \n\t\n   \nb",
			expected: "a\nb",
		},
		{
			name:     "trailing blank lines removed",
			input:    "a\nb\n\n\n",
			expected: "a\nb",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(removeBlankLines([]byte(tt.input)))
			if got != tt.expected {
				t.Errorf("removeBlankLines():\n  got:  %q\n  want: %q", got, tt.expected)
			}
		})
	}
}

func TestCleanConsoleLog(t *testing.T) {
	// Integration test: a snippet that exercises all transformations.
	input := "\x1b[0m\x1b[30m\x1b[40m\x1b[2J\x1b[01;01H\x1b[0m\x1b[37m\x1b[40mGRUB version 2.06\r\n" +
		"\x1b[04;02Hmenu items\r\n" +
		"\x1b[0m\x1b[30m\x1b[40m\x1b[2J  Booting `RHCOS (ostree:0)'\r\n" +
		"\r\n" +
		"[    0.000000] Linux version 5.14.0\r\n" +
		"\r\n\r\n\r\n\r\n\r\n" +
		"[\x1b[0;32m  OK  \x1b[0m] Started \x1b[0;1;39mJournal\x1b[0m.\r\n"

	got := string(CleanConsoleLog([]byte(input)))

	// Should start with the Booting marker (GRUB junk stripped).
	if !strings.HasPrefix(got, "Booting `RHCOS (ostree:0)'") {
		t.Errorf("expected output to start with Booting line, got:\n%s", got[:min(len(got), 200)])
	}

	// Should not contain any ESC characters.
	if strings.Contains(got, "\x1b") {
		t.Error("output still contains ESC characters")
	}

	// Should not contain carriage returns.
	if strings.Contains(got, "\r") {
		t.Error("output still contains carriage returns")
	}

	// Should contain the cleaned systemd OK line.
	if !strings.Contains(got, "[  OK  ] Started Journal.") {
		t.Errorf("expected cleaned systemd line, got:\n%s", got)
	}

	// The 5 blank lines should all be removed.
	if strings.Contains(got, "\n\n") {
		t.Error("blank lines were not removed")
	}
}
