package utils

import (
	"testing"

	"gotest.tools/assert"
)

func TestAnyToString(t *testing.T) {
	assert.Equal(t, "foo", AnyToString("foo"))
	assert.Equal(t, "42", AnyToString(42))
	assert.Equal(t, "true", AnyToString(true))
	assert.Equal(t, "3.14", AnyToString(3.14))
}
