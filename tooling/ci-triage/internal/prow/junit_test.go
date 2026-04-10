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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseJUnitBasic(t *testing.T) {
	xml := `<testsuite>
		<testcase name="TestPass"/>
		<testcase name="TestFail">
			<failure message="expected 1 got 2"/>
		</testcase>
		<testcase name="TestSkipped">
			<skipped/>
		</testcase>
	</testsuite>`

	result := ParseJUnit([]byte(xml))
	require.NotNil(t, result)
	assert.Len(t, result.Failures, 1)
	assert.Equal(t, "TestFail", result.Failures[0].Name)
	assert.Equal(t, "expected 1 got 2", result.Failures[0].Message)
	assert.Equal(t, []string{"TestPass"}, result.Passed)
}

func TestParseJUnitNestedSuites(t *testing.T) {
	xml := `<testsuites>
		<testsuite name="outer">
			<testsuite name="inner">
				<testcase name="TestNested">
					<failure message="nested fail"/>
				</testcase>
			</testsuite>
		</testsuite>
	</testsuites>`

	result := ParseJUnit([]byte(xml))
	require.NotNil(t, result)
	assert.Len(t, result.Failures, 1)
	assert.Equal(t, "TestNested", result.Failures[0].Name)
}

func TestParseJUnitFailureText(t *testing.T) {
	xml := `<testsuite>
		<testcase name="TestFail">
			<failure>error text body</failure>
		</testcase>
	</testsuite>`

	result := ParseJUnit([]byte(xml))
	require.NotNil(t, result)
	assert.Equal(t, "error text body", result.Failures[0].Message)
}

func TestParseJUnitEmpty(t *testing.T) {
	assert.Nil(t, ParseJUnit(nil))
	assert.Nil(t, ParseJUnit([]byte{}))
	assert.Nil(t, ParseJUnit([]byte("not xml")))
}

func TestParseJUnitTruncation(t *testing.T) {
	longMsg := strings.Repeat("x", 5000)
	xml := `<testsuite><testcase name="T"><failure message="` + longMsg + `"/></testcase></testsuite>`

	result := ParseJUnit([]byte(xml))
	require.NotNil(t, result)
	assert.Len(t, result.Failures[0].Message, 4000)
}

func TestStripAddresses(t *testing.T) {
	input := "error at 0xc0001a2b3c4d and 0x00000000deadbeef"
	result := StripAddresses(input)
	assert.Equal(t, "error at 0x... and 0x...", result)
}

func TestStripAddressesShort(t *testing.T) {
	input := "error at 0x1234"
	assert.Equal(t, input, StripAddresses(input))
}

func TestStripCertBytes(t *testing.T) {
	input := "cert: [48, 130, 4, 123, 48, 130, 3, 99, 160, 3, 2, 1, 2, 2, 16]"
	result := StripCertBytes(input)
	assert.Equal(t, "cert: [<cert-bytes>]", result)
}

func TestStripCertBytesTruncated(t *testing.T) {
	// Truncated array without closing bracket
	input := "cert: [48, 130, 4, 123, 48, 130, 3, 99, 160, 3, 2, 1, 2, 2, 16, 200, 100"
	result := StripCertBytes(input)
	assert.Equal(t, "cert: [<cert-bytes>]", result)
}

func TestStripCertBytesShortArray(t *testing.T) {
	input := "data: [1, 2, 3]"
	assert.Equal(t, input, StripCertBytes(input))
}

func TestStripANSI(t *testing.T) {
	input := "\x1b[31merror\x1b[0m: something failed"
	assert.Equal(t, "error: something failed", StripANSI(input))
}

func TestCleanBuildLog(t *testing.T) {
	input := "\x1b[31mline1\\nline2\\t\\\"quoted\\\"\x1b[0m"
	result := CleanBuildLog(input)
	assert.Contains(t, result, "line1\nline2\t\"quoted\"")
	assert.NotContains(t, result, "\x1b")
}

func TestParseJUnitStepLevel(t *testing.T) {
	// Use a valid XML string with the replacement char (U+FFFD) ANSI variant
	// that the Python tool's regex also handles
	xmlData := []byte(`<testsuite><testcase name="step-test"><failure message="` + "\ufffd" + `[31mansi error` + "\ufffd" + `[0m"/></testcase></testsuite>`)

	result := ParseJUnitStepLevel(xmlData)
	require.NotNil(t, result)
	assert.Equal(t, "ansi error", result.Failures[0].Message)
}
