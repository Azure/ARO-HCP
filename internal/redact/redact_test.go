package redact_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-HCP/internal/redact"
)

const (
	secretVal    = "thisIsASecret"
	nonSecretVal = "thisIsAStandardVal"
)

var (
	secretPtrVal = "thisIsAPtrSecret"
)

type StringType string

type TestStruct struct {
	Secret           string
	SecretStringType StringType
	SecretPtr        *string
	NonSecret        string `redact:"nonsecret"`
	unexported       string
	unexportedMap    map[string]string
}

type TestStructList struct {
	Data               []*TestStruct
	StringSliceData    []string
	IntSliceData       []int
	StringPtrSliceData []*string
}

type TestMaps struct {
	Secrets           map[string]string
	SecretPtrs        map[string]*string
	TestStructSecrets map[string]*TestStruct
}

type TestMapList struct {
	Data []*TestMaps
}

type TestNestedStruct struct {
	TestStruct    TestStruct
	TestStructPtr *TestStruct
}

func TestStringTestStruct(t *testing.T) {
	t.Run("Basic Secret Redaction", func(t *testing.T) {
		tStruct := &TestStruct{
			NonSecret:        nonSecretVal,
			Secret:           secretVal,
			SecretStringType: secretVal,
			SecretPtr:        &secretPtrVal,
			unexported:       nonSecretVal,
			unexportedMap:    map[string]string{"": ""},
		}

		err := redact.Redact(tStruct)
		assert.NoError(t, err, "should not fail to redact struct")

		assert.Equal(t, nonSecretVal, tStruct.NonSecret, "should contain non secret value")
		assert.Equal(t, redact.RedactStrConst, tStruct.Secret, "should redact secret value")
		assert.Equal(t, StringType(redact.RedactStrConst), tStruct.SecretStringType, "should redact secret value")
		assert.Equal(t, redact.RedactStrConst, *tStruct.SecretPtr, "should redact secret value")
		assert.Equal(t, nonSecretVal, tStruct.unexported, "should contain non secret value")
	})

	t.Run("Should still redact empty strings", func(t *testing.T) {
		emptyStrVal := ""

		tStruct := &TestStruct{
			NonSecret: nonSecretVal,
			Secret:    "",
			SecretPtr: &emptyStrVal,
		}

		err := redact.Redact(tStruct)
		assert.NoError(t, err, "should not fail to redact struct")

		assert.Equal(t, nonSecretVal, tStruct.NonSecret, "should contain non secret value")
		assert.Equal(t, redact.RedactStrConst, tStruct.Secret, "should redact secret value")
		assert.Equal(t, redact.RedactStrConst, *tStruct.SecretPtr, "should redact secret value")
	})

}

func TestStringTestStructList(t *testing.T) {
	t.Run("Basic Secret Redaction", func(t *testing.T) {
		tStruct := &TestStruct{
			NonSecret: nonSecretVal,
			Secret:    secretVal,
			SecretPtr: &secretPtrVal,
		}

		list := &TestStructList{
			Data:               []*TestStruct{tStruct},
			StringSliceData:    []string{secretVal},
			IntSliceData:       []int{0},
			StringPtrSliceData: []*string{&secretPtrVal, nil},
		}

		err := redact.Redact(list)
		assert.NoError(t, err, "should not fail to redact struct")

		assert.Equal(t, nonSecretVal, list.Data[0].NonSecret, "should contain non secret value")
		assert.Equal(t, redact.RedactStrConst, list.Data[0].Secret, "should redact secret value")
		assert.Equal(t, redact.RedactStrConst, *list.Data[0].SecretPtr, "should redact secret value")
		assert.Equal(t, redact.RedactStrConst, list.StringSliceData[0], "should redact secret value")
		assert.Equal(t, redact.RedactStrConst, *list.StringPtrSliceData[0], "should redact secret value")
	})

	t.Run("Should still redact empty strings", func(t *testing.T) {
		emptyStrVal := ""

		tStruct := &TestStruct{
			NonSecret: nonSecretVal,
			Secret:    "",
			SecretPtr: &emptyStrVal,
		}

		list := &TestStructList{
			Data:               []*TestStruct{tStruct},
			StringSliceData:    []string{""},
			IntSliceData:       []int{0},
			StringPtrSliceData: []*string{&[]string{""}[0], nil},
		}

		err := redact.Redact(list)
		assert.NoError(t, err, "should not fail to redact struct")

		assert.Equal(t, nonSecretVal, list.Data[0].NonSecret, "should contain non secret value")
		assert.Equal(t, redact.RedactStrConst, list.Data[0].Secret, "should redact secret value")
		assert.Equal(t, redact.RedactStrConst, *list.Data[0].SecretPtr, "should redact secret value")
		assert.Equal(t, redact.RedactStrConst, list.StringSliceData[0], "should redact secret value")
		assert.Equal(t, redact.RedactStrConst, *list.StringPtrSliceData[0], "should redact secret value")
	})

}

func TestStringTestMapAndEmbedded(t *testing.T) {
	t.Run("Should Redact Map And Slice Structs", func(t *testing.T) {
		tMaps := &TestMaps{
			Secrets: map[string]string{
				"secret-key-old": secretVal,
				"secret-key-new": secretVal,
			},
			SecretPtrs: map[string]*string{
				"ptr-secret-key": &secretPtrVal,
			},
			TestStructSecrets: map[string]*TestStruct{
				"ptr-test-struct-key": {
					NonSecret: nonSecretVal,
					Secret:    secretVal,
					SecretPtr: &secretPtrVal,
				},
			},
		}

		err := redact.Redact(tMaps)
		assert.NoError(t, err, "should not fail to redact struct")

		assert.Equal(t, redact.RedactStrConst, tMaps.Secrets["secret-key-old"], "should redact secret value")
		assert.Equal(t, redact.RedactStrConst, tMaps.Secrets["secret-key-new"], "should redact secret value")
		assert.Equal(t, redact.RedactStrConst, *tMaps.SecretPtrs["ptr-secret-key"], "should redact secret value")
		assert.Equal(t, redact.RedactStrConst, tMaps.TestStructSecrets["ptr-test-struct-key"].Secret, "should redact secret value")
		assert.Equal(t, redact.RedactStrConst, *tMaps.TestStructSecrets["ptr-test-struct-key"].SecretPtr, "should redact secret value")
		assert.Equal(t, nonSecretVal, tMaps.TestStructSecrets["ptr-test-struct-key"].NonSecret, "should redact secret value")
	})

	t.Run("Should Redact Map And Slice Structs", func(t *testing.T) {
		tMaps := &TestMaps{
			Secrets: map[string]string{
				"secret-key-old": secretVal,
				"secret-key-new": secretVal,
			},
			SecretPtrs: map[string]*string{
				"ptr-secret-key": &secretPtrVal,
			},
			TestStructSecrets: map[string]*TestStruct{
				"ptr-test-struct-key": {
					NonSecret: nonSecretVal,
					Secret:    secretVal,
					SecretPtr: &secretPtrVal,
				},
			},
		}

		testMapList := &TestMapList{
			Data: []*TestMaps{tMaps},
		}

		err := redact.Redact(testMapList)
		assert.NoError(t, err, "should not fail to redact struct")

		assert.Equal(t, redact.RedactStrConst, testMapList.Data[0].Secrets["secret-key-old"], "should redact secret value")
		assert.Equal(t, redact.RedactStrConst, testMapList.Data[0].Secrets["secret-key-new"], "should redact secret value")
		assert.Equal(t, redact.RedactStrConst, *testMapList.Data[0].SecretPtrs["ptr-secret-key"], "should redact secret value")
		assert.Equal(t, redact.RedactStrConst, testMapList.Data[0].TestStructSecrets["ptr-test-struct-key"].Secret, "should redact secret value")
		assert.Equal(t, redact.RedactStrConst, *testMapList.Data[0].TestStructSecrets["ptr-test-struct-key"].SecretPtr, "should redact secret value")
		assert.Equal(t, nonSecretVal, testMapList.Data[0].TestStructSecrets["ptr-test-struct-key"].NonSecret, "should redact secret value")
	})
}

func TestNotSettable(t *testing.T) {
	err := redact.Redact(1)
	assert.Error(t, err)
}

func TestInterface(t *testing.T) {
	i := interface{}(&TestStruct{})
	err := redact.Redact(&i)
	assert.NoError(t, err)
	assert.Equal(t, i, &TestStruct{Secret: redact.RedactStrConst, SecretStringType: redact.RedactStrConst})
}

func TestNestedStructs(t *testing.T) {
	tns := &TestNestedStruct{
		TestStructPtr: &TestStruct{SecretPtr: &[]string{""}[0]},
	}
	err := redact.Redact(tns)
	assert.NoError(t, err)
	assert.Equal(t, tns, &TestNestedStruct{
		TestStruct:    TestStruct{Secret: redact.RedactStrConst, SecretStringType: redact.RedactStrConst, SecretPtr: nil},
		TestStructPtr: &TestStruct{Secret: redact.RedactStrConst, SecretStringType: redact.RedactStrConst, SecretPtr: &[]string{redact.RedactStrConst}[0]},
	})
}

func TestCustomRedactor(t *testing.T) {
	redact.AddRedactor("lower", strings.ToLower)
	s := &struct {
		S string `redact:"lower"`
	}{"DATA"}
	err := redact.Redact(s)
	assert.NoError(t, err)
	assert.Equal(t, s.S, "data")
}

func TestArray(t *testing.T) {
	tStruct := &struct {
		SecretStrings    [2]string
		NotSecretStrings [2]string `redact:"nonsecret"`
	}{}

	err := redact.Redact(tStruct)
	assert.NoError(t, err, "should not fail to redact struct")

	assert.Equal(t, "", tStruct.NotSecretStrings[0], "should contain non secret value")
	assert.Equal(t, "", tStruct.NotSecretStrings[1], "should contain non secret value")
	assert.Equal(t, redact.RedactStrConst, tStruct.SecretStrings[0], "should redact secret value")
	assert.Equal(t, redact.RedactStrConst, tStruct.SecretStrings[1], "should redact secret value")
}

func TestNoTraverse(t *testing.T) {
	tStruct := &struct {
		Opaque struct {
			Secret string
		} `redact:"notraverse"`
		Nested struct {
			Secret string
		}
	}{
		Opaque: struct{ Secret string }{Secret: "keep-me"},
		Nested: struct{ Secret string }{Secret: "redact-me"},
	}

	err := redact.Redact(tStruct)
	assert.NoError(t, err)
	assert.Equal(t, "keep-me", tStruct.Opaque.Secret, "notraverse should stop traversal into child fields")
	assert.Equal(t, redact.RedactStrConst, tStruct.Nested.Secret, "untagged nested fields should still be redacted")
}
