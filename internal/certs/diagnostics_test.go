package certs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildDiagnosticsSubject(t *testing.T) {
	subject := BuildDiagnosticsSubject()

	assert.Equal(t, DiagnosticsCommonName, subject.CommonName)
	assert.Equal(t, []string{DiagnosticsOrganization}, subject.Organization)
}
