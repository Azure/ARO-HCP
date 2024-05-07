package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
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
			if flags.String() != tt.expectString {
				t.Errorf("Expected flags.String() to be %q, got %q", tt.expectString, flags.String())
			}
			if flags.ReadOnly() != tt.expectReadOnly {
				t.Errorf("Expected flags.ReadOnly() to be %v, got %v", tt.expectReadOnly, flags.ReadOnly())
			}
			if flags.CanUpdate() != tt.expectCanUpdate {
				t.Errorf("Expected flags.CanUpdate() to be %v, got %v", tt.expectCanUpdate, flags.CanUpdate())
			}
			if flags.CaseInsensitive() != tt.expectCaseInsensitive {
				t.Errorf("Expected flags.CaseInsensitive() to be %v, got %v", tt.expectCaseInsensitive, flags.CaseInsensitive())
			}
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

	// For equality checks of various reflect types.
	E any `visibility:"read"`
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
		"E":                  reflect.StructTag("visibility:\"read\""),
	}

	// The test cases are encoded into the type itself.
	if !cmp.Equal(TestModelTypeStructTagMap, expectedStructTagMap, nil) {
		t.Errorf(
			"StructTagMap had unexpected differences:\n%s",
			cmp.Diff(expectedStructTagMap, TestModelTypeStructTagMap, nil))
	}
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
			name: "Test equality for bool type fields",
			v: TestModelType{
				E: Ptr(bool(true)),
			},
			w: TestModelType{
				E: Ptr(bool(true)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for bool type fields",
			v: TestModelType{
				E: Ptr(bool(true)),
			},
			w: TestModelType{
				E: Ptr(bool(false)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for int type fields",
			v: TestModelType{
				E: Ptr(int(1)),
			},
			w: TestModelType{
				E: Ptr(int(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for int type fields",
			v: TestModelType{
				E: Ptr(int(1)),
			},
			w: TestModelType{
				E: Ptr(int(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for int8 type fields",
			v: TestModelType{
				E: Ptr(int8(1)),
			},
			w: TestModelType{
				E: Ptr(int8(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for int8 type fields",
			v: TestModelType{
				E: Ptr(int8(1)),
			},
			w: TestModelType{
				E: Ptr(int8(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for int16 type fields",
			v: TestModelType{
				E: Ptr(int16(1)),
			},
			w: TestModelType{
				E: Ptr(int16(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for int16 type fields",
			v: TestModelType{
				E: Ptr(int16(1)),
			},
			w: TestModelType{
				E: Ptr(int16(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for int32 type fields",
			v: TestModelType{
				E: Ptr(int32(1)),
			},
			w: TestModelType{
				E: Ptr(int32(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for int32 type fields",
			v: TestModelType{
				E: Ptr(int32(1)),
			},
			w: TestModelType{
				E: Ptr(int32(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for int64 type fields",
			v: TestModelType{
				E: Ptr(int64(1)),
			},
			w: TestModelType{
				E: Ptr(int64(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for int64 type fields",
			v: TestModelType{
				E: Ptr(int64(1)),
			},
			w: TestModelType{
				E: Ptr(int64(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for uint type fields",
			v: TestModelType{
				E: Ptr(uint(1)),
			},
			w: TestModelType{
				E: Ptr(uint(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for uint type fields",
			v: TestModelType{
				E: Ptr(uint(1)),
			},
			w: TestModelType{
				E: Ptr(uint(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for uintptr type fields",
			v: TestModelType{
				E: Ptr(uintptr(1)),
			},
			w: TestModelType{
				E: Ptr(uintptr(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for uintptr type fields",
			v: TestModelType{
				E: Ptr(uintptr(1)),
			},
			w: TestModelType{
				E: Ptr(uintptr(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for uint8 type fields",
			v: TestModelType{
				E: Ptr(uint8(1)),
			},
			w: TestModelType{
				E: Ptr(uint8(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for uint8 type fields",
			v: TestModelType{
				E: Ptr(uint8(1)),
			},
			w: TestModelType{
				E: Ptr(uint8(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for uint16 type fields",
			v: TestModelType{
				E: Ptr(uint16(1)),
			},
			w: TestModelType{
				E: Ptr(uint16(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for uint16 type fields",
			v: TestModelType{
				E: Ptr(uint16(1)),
			},
			w: TestModelType{
				E: Ptr(uint16(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for uint32 type fields",
			v: TestModelType{
				E: Ptr(uint32(1)),
			},
			w: TestModelType{
				E: Ptr(uint32(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for uint32 type fields",
			v: TestModelType{
				E: Ptr(uint32(1)),
			},
			w: TestModelType{
				E: Ptr(uint32(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for uint64 type fields",
			v: TestModelType{
				E: Ptr(uint64(1)),
			},
			w: TestModelType{
				E: Ptr(uint64(1)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for uint64 type fields",
			v: TestModelType{
				E: Ptr(uint64(1)),
			},
			w: TestModelType{
				E: Ptr(uint64(0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for float32 type fields",
			v: TestModelType{
				E: Ptr(float32(1.0)),
			},
			w: TestModelType{
				E: Ptr(float32(1.0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for float32 type fields",
			v: TestModelType{
				E: Ptr(float32(1.0)),
			},
			w: TestModelType{
				E: Ptr(float32(0.0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for float64 type fields",
			v: TestModelType{
				E: Ptr(float64(1.0)),
			},
			w: TestModelType{
				E: Ptr(float64(1.0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for float64 type fields",
			v: TestModelType{
				E: Ptr(float64(1.0)),
			},
			w: TestModelType{
				E: Ptr(float64(0.0)),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for complex64 type fields",
			v: TestModelType{
				E: Ptr(complex(float32(1.0), float32(-1.0))),
			},
			w: TestModelType{
				E: Ptr(complex(float32(1.0), float32(-1.0))),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for complex64 type fields",
			v: TestModelType{
				E: Ptr(complex(float32(1.0), float32(-1.0))),
			},
			w: TestModelType{
				E: Ptr(complex(float32(0.0), float32(1.0))),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for complex128 type fields",
			v: TestModelType{
				E: Ptr(complex(float64(1.0), float64(-1.0))),
			},
			w: TestModelType{
				E: Ptr(complex(float64(1.0), float64(-1.0))),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality for complex128 type fields",
			v: TestModelType{
				E: Ptr(complex(float64(1.0), float64(-1.0))),
			},
			w: TestModelType{
				E: Ptr(complex(float64(0.0), float64(1.0))),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test equality for slice fields",
			v: TestModelType{
				E: []int{1, 2, 3},
			},
			w: TestModelType{
				E: []int{1, 2, 3},
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality due to nil pointer for slice fields",
			v: TestModelType{
				E: []int{1, 2, 3},
			},
			w: TestModelType{
				E: []int(nil),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test inequality due to length for slice fields",
			v: TestModelType{
				E: []int{1, 2, 3},
			},
			w: TestModelType{
				E: []int{1},
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test inequality due to content for slice fields",
			v: TestModelType{
				E: []int{3, 2, 1},
			},
			w: TestModelType{
				E: []int{1, 2, 3},
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 2, // error for each changed element
		},
		{
			name: "Test equality for array fields",
			v: TestModelType{
				E: [3]int{1, 2, 3},
			},
			w: TestModelType{
				E: [3]int{1, 2, 3},
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 0,
		},
		{
			name: "Test inequality due for array fields",
			v: TestModelType{
				E: [3]int{3, 2, 1},
			},
			w: TestModelType{
				E: [3]int{1, 2, 3},
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 2, // error for each changed element
		},
		{
			name: "Test equality for map fields",
			v: TestModelType{
				E: map[string]string{
					"Blinky": "Shadow",
					"Pinky":  "Speedy",
					"Inky":   "Bashful",
					"Clyde":  "Pokey",
				},
			},
			w: TestModelType{
				E: map[string]string{
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
				E: map[string]string{
					"Blinky": "Shadow",
					"Pinky":  "Speedy",
					"Inky":   "Bashful",
					"Clyde":  "Pokey",
				},
			},
			w: TestModelType{
				E: map[string]string(nil),
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test inequality due to length for map fields",
			v: TestModelType{
				E: map[string]string{
					"Blinky": "Shadow",
					"Pinky":  "Speedy",
					"Inky":   "Bashful",
					"Clyde":  "Pokey",
				},
			},
			w: TestModelType{
				E: map[string]string{
					"Blinky": "Shadow",
				},
			},
			m:              TestModelTypeStructTagMap,
			updating:       false,
			errorsExpected: 1,
		},
		{
			name: "Test inequality due to content for map fields",
			v: TestModelType{
				E: map[string]string{
					"Akabei": "Oikake",
					"Pinky":  "Machibuse",
					"Aosuke": "Kimagure",
					"Guzuta": "Otoboke",
				},
			},
			w: TestModelType{
				E: map[string]string{
					"Blinky": "Shadow",
					"Pinky":  "Speedy",
					"Inky":   "Bashful",
					"Clyde":  "Pokey",
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
			if len(cloudErrors) != tt.errorsExpected {
				t.Errorf("Expected %d errors, got %d: %v", tt.errorsExpected, len(cloudErrors), cloudErrors)
			}
		})
	}
}
