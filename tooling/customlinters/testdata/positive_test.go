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

package testdata

import (
	"fmt"
	"log"
	"testing"
)

// This file contains positive test cases - code that SHOULD trigger the linter

func TestWithFmtPrintln(t *testing.T) {
	// Should be flagged
	fmt.Println("This should be flagged") // want "do not use fmt.Println in test files"
}

func TestWithFmtPrintf(t *testing.T) {
	// Should be flagged
	fmt.Printf("This too: %s\n", "flagged") // want "do not use fmt.Printf in test files"
}

func TestWithFmtPrint(t *testing.T) {
	// Should be flagged
	fmt.Print("Also flagged") // want "do not use fmt.Print in test files"
}

func TestWithLogPrintln(t *testing.T) {
	// Should be flagged
	log.Println("This should be flagged") // want "do not use log.Println in test files"
}

func TestWithBuiltinPrintln(t *testing.T) {
	// Should be flagged
	println("And this builtin") // want "do not use builtin println in test files"
}

func TestWithBuiltinPrint(t *testing.T) {
	// Should be flagged
	print("Also builtin") // want "do not use builtin print in test files"
}
