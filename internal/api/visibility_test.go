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

package api

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVisibilityFlags(t *testing.T) {
	tests := []struct {
		name                  string
		tag                   reflect.StructTag
		expectString          string
		expectReadOnly        bool
		expectCanUpdate       bool
		expectCaseInsensitive bool
	}{
		{
			name:                  "Visibility: (none)",
			tag:                   reflect.StructTag("visibility:\"\""),
			expectString:          "",
			expectReadOnly:        false,
			expectCanUpdate:       false,
			expectCaseInsensitive: false,
		},
		{
			name:                  "Visibility: read",
			tag:                   reflect.StructTag("visibility:\"read\""),
			expectString:          "read",
			expectReadOnly:        true,
			expectCanUpdate:       false,
			expectCaseInsensitive: false,
		},
		{
			name:                  "Visibility: create",
			tag:                   reflect.StructTag("visibility:\"create\""),
			expectString:          "create",
			expectReadOnly:        false,
			expectCanUpdate:       false,
			expectCaseInsensitive: false,
		},
		{
			name:                  "Visibility: update",
			tag:                   reflect.StructTag("visibility:\"update\""),
			expectString:          "update",
			expectReadOnly:        false,
			expectCanUpdate:       true,
			expectCaseInsensitive: false,
		},
		{
			name:                  "Visibility: nocase",
			tag:                   reflect.StructTag("visibility:\"nocase\""),
			expectString:          "nocase",
			expectReadOnly:        false,
			expectCanUpdate:       false,
			expectCaseInsensitive: true,
		},
		{
			name:                  "Visibility: (all)",
			tag:                   reflect.StructTag("visibility:\"read create update nocase\""),
			expectString:          "read create update nocase",
			expectReadOnly:        false,
			expectCanUpdate:       true,
			expectCaseInsensitive: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags, _ := GetVisibilityFlags(tt.tag)
			assert.Equal(t, tt.expectString, flags.String())
			assert.Equal(t, tt.expectReadOnly, flags.ReadOnly())
			assert.Equal(t, tt.expectCanUpdate, flags.CanUpdate())
			assert.Equal(t, tt.expectCaseInsensitive, flags.CaseInsensitive())
		})
	}
}

type TestModelType struct {
	// Subtype inherits default visibility.
	A *TestModelSubtype

	// Subtype inherits explicit visibility.
	B *TestModelSubtype `visibility:"read"`

	// Slice of simple type inherits visibility.
	C []*string `visibility:"read"`

	// Slice of struct type inherits visibility but can override.
	D []*TestModelSubtype `visibility:"update nocase"`

	// Map of struct type inherits visibility but can override.
	E map[string]*TestModelSubtype `visibility:"read create update nocase"`

	// For equality checks of various reflect types.
	F any `visibility:"read"`
}

type TestModelSubtype struct {
	Implicit         *string
	Read             *string `visibility:"read"`
	ReadNoCase       *string `visibility:"read nocase"`
	ReadCreate       *string `visibility:"read create"`
	ReadCreateUpdate *string `visibility:"read create update"`
}

var (
	TestModelTypeStructTagMap    = NewStructTagMap[TestModelType]()
	TestModelSubtypeStructTagMap = NewStructTagMap[TestModelSubtype]()
)

func TestStructTagMap(t *testing.T) {
	expectedStructTagMap := StructTagMap{
		"A.Read":             reflect.StructTag("visibility:\"read\""),
		"A.ReadNoCase":       reflect.StructTag("visibility:\"read nocase\""),
		"A.ReadCreate":       reflect.StructTag("visibility:\"read create\""),
		"A.ReadCreateUpdate": reflect.StructTag("visibility:\"read create update\""),
		"B":                  reflect.StructTag("visibility:\"read\""),
		"B.Read":             reflect.StructTag("visibility:\"read\""),
		"B.ReadNoCase":       reflect.StructTag("visibility:\"read nocase\""),
		"B.ReadCreate":       reflect.StructTag("visibility:\"read create\""),
		"B.ReadCreateUpdate": reflect.StructTag("visibility:\"read create update\""),
		"C":                  reflect.StructTag("visibility:\"read\""),
		"D":                  reflect.StructTag("visibility:\"update nocase\""),
		"D.Read":             reflect.StructTag("visibility:\"read\""),
		"D.ReadNoCase":       reflect.StructTag("visibility:\"read nocase\""),
		"D.ReadCreate":       reflect.StructTag("visibility:\"read create\""),
		"D.ReadCreateUpdate": reflect.StructTag("visibility:\"read create update\""),
		"E":                  reflect.StructTag("visibility:\"read create update nocase\""),
		"E.Read":             reflect.StructTag("visibility:\"read\""),
		"E.ReadNoCase":       reflect.StructTag("visibility:\"read nocase\""),
		"E.ReadCreate":       reflect.StructTag("visibility:\"read create\""),
		"E.ReadCreateUpdate": reflect.StructTag("visibility:\"read create update\""),
		"F":                  reflect.StructTag("visibility:\"read\""),
	}

	// The test cases are encoded into the type itself.
	assert.Equal(t, expectedStructTagMap, TestModelTypeStructTagMap)
}

func TestValidateVisibility(t *testing.T) {
	testValues := TestModelSubtype{
		Implicit:         Ptr("cherry"),
		Read:             Ptr("strawberry"),
		ReadNoCase:       Ptr("peach"),
		ReadCreate:       Ptr("apple"),
		ReadCreateUpdate: Ptr("melon"),
	}

	testImplicitVisibility := TestModelType{
		A: &testValues,
		B: &testValues,
	}

	tests := []struct {
		name           string
		v              any
		w              any
		m              StructTagMap
		updating       bool
		errorsExpected int
	}{
		{
			// Required fields are out of scope for visibility tests.
			name:           "Create: Empty structure is accepted",
			v:              TestModelSubtype{},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Create: Set read-only field to same value is accepted",
			v: TestModelSubtype{
				Read: Ptr("strawberry"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Create: Set read-only field to same but differently cased value is rejected",
			v: TestModelSubtype{
				Read: Ptr("STRAWBERRY"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Create: Set read-only field to different value is rejected",
			v: TestModelSubtype{
				Read: Ptr("pretzel"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Create: Set case-insensitive read-only field to same value is accepted",
			v: TestModelSubtype{
				ReadNoCase: Ptr("peach"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Create: Set case-insensitive read-only field to same but differently cased value is accepted",
			v: TestModelSubtype{
				ReadNoCase: Ptr("PEACH"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Create: Set case-insensitive read-only field to different value is rejected",
			v: TestModelSubtype{
				ReadNoCase: Ptr("pretzel"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Create: Set read/create field to same value is accepted",
			v: TestModelSubtype{
				ReadCreate: Ptr("apple"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Create: Set read/create field to different value is accepted",
			v: TestModelSubtype{
				ReadCreate: Ptr("pear"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Create: Set read/create/update field to same value is accepted",
			v: TestModelSubtype{
				ReadCreateUpdate: Ptr("melon"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Create: Set read/create/update field to different value is accepted",
			v: TestModelSubtype{
				ReadCreateUpdate: Ptr("banana"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			// Required fields are out of scope for visibility tests.
			name:           "Update: Empty structure is accepted",
			v:              TestModelSubtype{},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       true,
			errorsExpected: 0,
		},
		{
			name: "Update: Set read-only field to same value is accepted",
			v: TestModelSubtype{
				Read: Ptr("strawberry"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       true,
			errorsExpected: 0,
		},
		{
			name: "Update: Set read-only field to same but differently cased value is rejected",
			v: TestModelSubtype{
				Read: Ptr("STRAWBERRY"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       true,
			errorsExpected: 1,
		},
		{
			name: "Update: Set read-only field to different value is rejected",
			v: TestModelSubtype{
				Read: Ptr("pretzel"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       true,
			errorsExpected: 1,
		},
		{
			name: "Update: Set case-insensitive read-only field to same value is accepted",
			v: TestModelSubtype{
				ReadNoCase: Ptr("peach"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       true,
			errorsExpected: 0,
		},
		{
			name: "Update: Set case-insensitive read-only field to same but differently cased value is accepted",
			v: TestModelSubtype{
				ReadNoCase: Ptr("PEACH"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       true,
			errorsExpected: 0,
		},
		{
			name: "Update: Set case-insensitive read-only field to different value is rejected",
			v: TestModelSubtype{
				ReadNoCase: Ptr("pretzel"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       true,
			errorsExpected: 1,
		},
		{
			name: "Update: Set read/create field to same value is accepted",
			v: TestModelSubtype{
				ReadCreate: Ptr("apple"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       true,
			errorsExpected: 0,
		},
		{
			name: "Update: Set read/create field to different value is rejected",
			v: TestModelSubtype{
				ReadCreate: Ptr("pear"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       true,
			errorsExpected: 1,
		},
		{
			name: "Update: Set read/create/update field to same value is accepted",
			v: TestModelSubtype{
				ReadCreateUpdate: Ptr("melon"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       true,
			errorsExpected: 0,
		},
		{
			name: "Update: Set read/create/update field to different value is accepted",
			v: TestModelSubtype{
				ReadCreateUpdate: Ptr("banana"),
			},
			w:              testValues,
			m:              TestModelSubtypeStructTagMap,
			updating:       true,
			errorsExpected: 0,
		},
		{
			name: "Set implicit read/create/update field to same value is accepted",
			v: TestModelType{
				A: &TestModelSubtype{
					Implicit: Ptr("cherry"),
				},
			},
			w:              testImplicitVisibility,
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Set implicit read/create/update field to different value is accepted",
			v: TestModelType{
				A: &TestModelSubtype{
					Implicit: Ptr("bell"),
				},
			},
			w:              testImplicitVisibility,
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Set implicit read-only field to same value is accepted",
			v: TestModelType{
				B: &TestModelSubtype{
					Implicit: Ptr("cherry"),
				},
			},
			w:              testImplicitVisibility,
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Set implicit read-only field to different value is rejected",
			v: TestModelType{
				B: &TestModelSubtype{
					Implicit: Ptr("bell"),
				},
			},
			w:              testImplicitVisibility,
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Add map key with read-only value to nil map is accepted for zero value",
			v: TestModelType{
				E: map[string]*TestModelSubtype{
					"1up": &TestModelSubtype{},
				},
			},
			w:              TestModelType{},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Add map key with read-only value to nil map is rejected for non-zero value",
			v: TestModelType{
				E: map[string]*TestModelSubtype{
					"1up": &testValues,
				},
			},
			w:              TestModelType{},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 2, // testValues has two read-only fields
		},
		{
			name: "Add map key with read-only value to existing map is accepted for zero value",
			v: TestModelType{
				E: map[string]*TestModelSubtype{
					"1up": &TestModelSubtype{},
				},
			},
			w: TestModelType{
				E: map[string]*TestModelSubtype{
					"2up": &testValues,
				},
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Add map key with read-only value to existing map is rejected for non-zero value",
			v: TestModelType{
				E: map[string]*TestModelSubtype{
					"1up": &testValues,
				},
			},
			w: TestModelType{
				E: map[string]*TestModelSubtype{
					"2up": &testValues,
				},
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 2, // testValues has two read-only fields
		},
		{
			name: "Test equality for bool type fields",
			v: TestModelType{
				F: Ptr(bool(true)),
			},
			w: TestModelType{
				F: Ptr(bool(true)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for bool type fields",
			v: TestModelType{
				F: Ptr(bool(true)),
			},
			w: TestModelType{
				F: Ptr(bool(false)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for int type fields",
			v: TestModelType{
				F: Ptr(int(1)),
			},
			w: TestModelType{
				F: Ptr(int(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for int type fields",
			v: TestModelType{
				F: Ptr(int(1)),
			},
			w: TestModelType{
				F: Ptr(int(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for int8 type fields",
			v: TestModelType{
				F: Ptr(int8(1)),
			},
			w: TestModelType{
				F: Ptr(int8(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for int8 type fields",
			v: TestModelType{
				F: Ptr(int8(1)),
			},
			w: TestModelType{
				F: Ptr(int8(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for int16 type fields",
			v: TestModelType{
				F: Ptr(int16(1)),
			},
			w: TestModelType{
				F: Ptr(int16(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for int16 type fields",
			v: TestModelType{
				F: Ptr(int16(1)),
			},
			w: TestModelType{
				F: Ptr(int16(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for int32 type fields",
			v: TestModelType{
				F: Ptr(int32(1)),
			},
			w: TestModelType{
				F: Ptr(int32(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for int32 type fields",
			v: TestModelType{
				F: Ptr(int32(1)),
			},
			w: TestModelType{
				F: Ptr(int32(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for int64 type fields",
			v: TestModelType{
				F: Ptr(int64(1)),
			},
			w: TestModelType{
				F: Ptr(int64(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for int64 type fields",
			v: TestModelType{
				F: Ptr(int64(1)),
			},
			w: TestModelType{
				F: Ptr(int64(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for uint type fields",
			v: TestModelType{
				F: Ptr(uint(1)),
			},
			w: TestModelType{
				F: Ptr(uint(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for uint type fields",
			v: TestModelType{
				F: Ptr(uint(1)),
			},
			w: TestModelType{
				F: Ptr(uint(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for uintptr type fields",
			v: TestModelType{
				F: Ptr(uintptr(1)),
			},
			w: TestModelType{
				F: Ptr(uintptr(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for uintptr type fields",
			v: TestModelType{
				F: Ptr(uintptr(1)),
			},
			w: TestModelType{
				F: Ptr(uintptr(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for uint8 type fields",
			v: TestModelType{
				F: Ptr(uint8(1)),
			},
			w: TestModelType{
				F: Ptr(uint8(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for uint8 type fields",
			v: TestModelType{
				F: Ptr(uint8(1)),
			},
			w: TestModelType{
				F: Ptr(uint8(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for uint16 type fields",
			v: TestModelType{
				F: Ptr(uint16(1)),
			},
			w: TestModelType{
				F: Ptr(uint16(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for uint16 type fields",
			v: TestModelType{
				F: Ptr(uint16(1)),
			},
			w: TestModelType{
				F: Ptr(uint16(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for uint32 type fields",
			v: TestModelType{
				F: Ptr(uint32(1)),
			},
			w: TestModelType{
				F: Ptr(uint32(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for uint32 type fields",
			v: TestModelType{
				F: Ptr(uint32(1)),
			},
			w: TestModelType{
				F: Ptr(uint32(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for uint64 type fields",
			v: TestModelType{
				F: Ptr(uint64(1)),
			},
			w: TestModelType{
				F: Ptr(uint64(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for uint64 type fields",
			v: TestModelType{
				F: Ptr(uint64(1)),
			},
			w: TestModelType{
				F: Ptr(uint64(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for float32 type fields",
			v: TestModelType{
				F: Ptr(float32(1.0)),
			},
			w: TestModelType{
				F: Ptr(float32(1.0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for float32 type fields",
			v: TestModelType{
				F: Ptr(float32(1.0)),
			},
			w: TestModelType{
				F: Ptr(float32(0.0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for float64 type fields",
			v: TestModelType{
				F: Ptr(float64(1.0)),
			},
			w: TestModelType{
				F: Ptr(float64(1.0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for float64 type fields",
			v: TestModelType{
				F: Ptr(float64(1.0)),
			},
			w: TestModelType{
				F: Ptr(float64(0.0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for complex64 type fields",
			v: TestModelType{
				F: Ptr(complex(float32(1.0), float32(-1.0))),
			},
			w: TestModelType{
				F: Ptr(complex(float32(1.0), float32(-1.0))),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for complex64 type fields",
			v: TestModelType{
				F: Ptr(complex(float32(1.0), float32(-1.0))),
			},
			w: TestModelType{
				F: Ptr(complex(float32(0.0), float32(1.0))),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for complex128 type fields",
			v: TestModelType{
				F: Ptr(complex(float64(1.0), float64(-1.0))),
			},
			w: TestModelType{
				F: Ptr(complex(float64(1.0), float64(-1.0))),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for complex128 type fields",
			v: TestModelType{
				F: Ptr(complex(float64(1.0), float64(-1.0))),
			},
			w: TestModelType{
				F: Ptr(complex(float64(0.0), float64(1.0))),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for slice fields",
			v: TestModelType{
				F: []int{1, 2, 3},
			},
			w: TestModelType{
				F: []int{1, 2, 3},
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality due to nil pointer for slice fields",
			v: TestModelType{
				F: []int{1, 2, 3},
			},
			w: TestModelType{
				F: []int(nil),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test inequality due to length for slice fields",
			v: TestModelType{
				F: []int{1, 2, 3},
			},
			w: TestModelType{
				F: []int{1},
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test inequality due to content for slice fields",
			v: TestModelType{
				F: []int{3, 2, 1},
			},
			w: TestModelType{
				F: []int{1, 2, 3},
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 2, // error for each changed element
		},
		{
			name: "Test equality for array fields",
			v: TestModelType{
				F: [3]int{1, 2, 3},
			},
			w: TestModelType{
				F: [3]int{1, 2, 3},
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality due for array fields",
			v: TestModelType{
				F: [3]int{3, 2, 1},
			},
			w: TestModelType{
				F: [3]int{1, 2, 3},
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 2, // error for each changed element
		},
		{
			name: "Test equality for map fields",
			v: TestModelType{
				F: map[string]string{
					"Blinky": "Shadow",
					"Pinky":  "Speedy",
					"Inky":   "Bashful",
					"Clyde":  "Pokey",
				},
			},
			w: TestModelType{
				F: map[string]string{
					"Blinky": "Shadow",
					"Pinky":  "Speedy",
					"Inky":   "Bashful",
					"Clyde":  "Pokey",
				},
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality due to nil pointer for map fields",
			v: TestModelType{
				F: map[string]string{
					"Blinky": "Shadow",
					"Pinky":  "Speedy",
					"Inky":   "Bashful",
					"Clyde":  "Pokey",
				},
			},
			w: TestModelType{
				F: map[string]string(nil),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test inequality due to length for map fields",
			v: TestModelType{
				F: map[string]string{
					"Blinky": "Shadow",
					"Pinky":  "Speedy",
					"Inky":   "Bashful",
					"Clyde":  "Pokey",
				},
			},
			w: TestModelType{
				F: map[string]string{
					"Blinky": "Shadow",
				},
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test inequality due to content for map keys",
			v: TestModelType{
				F: map[string]string{
					"Akabei": "Oikake",
					"Pinky":  "Machibuse",
					"Aosuke": "Kimagure",
					"Guzuta": "Otoboke",
				},
			},
			w: TestModelType{
				F: map[string]string{
					"Blinky": "Shadow",
					"Pinky":  "Speedy",
					"Inky":   "Bashful",
					"Clyde":  "Pokey",
				},
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test inequality due to content for map values",
			v: TestModelType{
				F: map[string]string{
					"Blinky": "Shadow",
					"Pinky":  "Speedy",
					"Inky":   "Bashful",
					"Clyde":  "Pokey",
				},
			},
			w: TestModelType{
				F: map[string]string{
					"Blinky": "Red",
					"Pinky":  "Pink",
					"Inky":   "Cyan",
					"Clyde":  "Orange",
				},
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 4, // error for each changed element
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cloudErrors := ValidateVisibility(tt.v, tt.w, tt.m, tt.updating)
			assert.Len(t, cloudErrors, tt.errorsExpected)
		})
	}
}
