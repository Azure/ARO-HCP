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

package prow

import (
	"encoding/xml"
	"regexp"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/config"
)

// TestFailure represents a single test failure from JUnit XML.
type TestFailure struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

// JUnitResult holds parsed JUnit test results.
type JUnitResult struct {
	Failures []TestFailure `json:"failures"`
	Passed   []string      `json:"passed"`
}

// junitTestSuites represents the root <testsuites> element.
type junitTestSuites struct {
	XMLName xml.Name         `xml:"testsuites"`
	Suites  []junitTestSuite `xml:"testsuite"`
}

// junitTestSuite represents a <testsuite> element.
type junitTestSuite struct {
	TestCases []junitTestCase  `xml:"testcase"`
	Suites    []junitTestSuite `xml:"testsuite"`
}

// junitTestCase represents a <testcase> element.
type junitTestCase struct {
	Name    string        `xml:"name,attr"`
	Failure *junitFailure `xml:"failure"`
	Skipped *struct{}     `xml:"skipped"`
}

// junitFailure represents a <failure> element.
type junitFailure struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

var (
	goPtrRE   = regexp.MustCompile(`0x[0-9a-f]{8,}`)
	certBytRE = regexp.MustCompile(`\[(?:\d{1,3},\s*){10,}\d{1,3}(?:\]|(?:,\s*\d{1,3})*\s*$)`)
	ansiRE    = regexp.MustCompile("(?:\x1b|\\x{fffd})\\[[0-9;]*m")
)

// StripANSI removes ANSI escape codes from a string.
func StripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

// StripAddresses replaces Go pointer hex addresses with "0x...".
func StripAddresses(msg string) string {
	return goPtrRE.ReplaceAllString(msg, "0x...")
}

// StripCertBytes replaces raw DER certificate byte arrays with a placeholder.
func StripCertBytes(msg string) string {
	return certBytRE.ReplaceAllString(msg, "[<cert-bytes>]")
}

// sanitizeMessage applies all message sanitization steps and truncates.
func sanitizeMessage(msg string) string {
	msg = StripAddresses(msg)
	msg = StripCertBytes(msg)
	if len(msg) > config.MaxMessageChars {
		msg = msg[:config.MaxMessageChars]
	}
	return msg
}

// ParseJUnit parses JUnit XML bytes into test failures and passed test names.
// Returns nil if data is empty or unparseable.
func ParseJUnit(data []byte) *JUnitResult {
	if len(data) == 0 {
		return nil
	}

	var testCases []junitTestCase

	// Try parsing as <testsuites> first
	var suites junitTestSuites
	if err := xml.Unmarshal(data, &suites); err == nil && len(suites.Suites) > 0 {
		testCases = collectTestCases(suites.Suites)
	} else {
		// Try as a single <testsuite>
		var suite junitTestSuite
		if err := xml.Unmarshal(data, &suite); err != nil {
			return nil
		}
		testCases = suite.TestCases
		// Also collect from nested suites
		testCases = append(testCases, collectTestCasesFromSuite(suite)...)
	}

	if len(testCases) == 0 {
		// Last attempt: try finding testcases at any level
		type rawRoot struct {
			TestCases []junitTestCase `xml:"testcase"`
		}
		var root rawRoot
		if err := xml.Unmarshal(data, &root); err != nil {
			return nil
		}
		testCases = root.TestCases
	}

	result := &JUnitResult{}
	for _, tc := range testCases {
		if tc.Failure != nil {
			rawMsg := tc.Failure.Message
			if rawMsg == "" {
				rawMsg = tc.Failure.Text
			}
			result.Failures = append(result.Failures, TestFailure{
				Name:    tc.Name,
				Message: sanitizeMessage(rawMsg),
			})
		} else if tc.Name != "" && tc.Skipped == nil {
			result.Passed = append(result.Passed, tc.Name)
		}
	}
	return result
}

// collectTestCases extracts all test cases from nested suites.
func collectTestCases(suites []junitTestSuite) []junitTestCase {
	var result []junitTestCase
	for _, s := range suites {
		result = append(result, s.TestCases...)
		result = append(result, collectTestCases(s.Suites)...)
	}
	return result
}

// collectTestCasesFromSuite extracts test cases from nested sub-suites only.
func collectTestCasesFromSuite(suite junitTestSuite) []junitTestCase {
	return collectTestCases(suite.Suites)
}

// ParseJUnitStepLevel parses step-level junit_operator.xml with ANSI stripping.
func ParseJUnitStepLevel(data []byte) *JUnitResult {
	result := ParseJUnit(data)
	if result == nil {
		return nil
	}
	for i := range result.Failures {
		result.Failures[i].Message = StripANSI(result.Failures[i].Message)
	}
	// Rename: step-level failures use "step" semantics but same struct
	return result
}

// CleanBuildLog strips ANSI codes and unescapes common escape sequences.
func CleanBuildLog(text string) string {
	text = StripANSI(text)
	text = strings.ReplaceAll(text, `\"`, `"`)
	text = strings.ReplaceAll(text, `\n`, "\n")
	text = strings.ReplaceAll(text, `\t`, "\t")
	return text
}
