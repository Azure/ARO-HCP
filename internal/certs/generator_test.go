package certs

import (
	"crypto/x509/pkix"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratePrivateKey(t *testing.T) {
	key, err := GeneratePrivateKey(2048)
	require.NoError(t, err)
	assert.NotNil(t, key)
	assert.Equal(t, 2048, key.N.BitLen())
}

func TestGenerateCSR(t *testing.T) {
	key, err := GeneratePrivateKey(2048)
	require.NoError(t, err)

	subject := pkix.Name{
		CommonName:   "test-user",
		Organization: []string{"test-org"},
	}

	csrPEM, err := GenerateCSR(key, subject)
	require.NoError(t, err)
	assert.Contains(t, string(csrPEM), "CERTIFICATE REQUEST")
}

func TestEncodePrivateKey(t *testing.T) {
	key, err := GeneratePrivateKey(2048)
	require.NoError(t, err)

	pem := EncodePrivateKey(key)
	assert.Contains(t, string(pem), "RSA PRIVATE KEY")
}

func TestBuildSubject(t *testing.T) {
	tests := []struct {
		name       string
		user       string
		privileged bool
		wantCN     string
		wantOrg    string
	}{
		{
			name:       "unprivileged",
			user:       "testuser",
			privileged: false,
			wantCN:     "system:sre-break-glass:testuser",
			wantOrg:    "aro-sre",
		},
		{
			name:       "privileged",
			user:       "testuser",
			privileged: true,
			wantCN:     "system:sre-break-glass:testuser",
			wantOrg:    "aro-sre-cluster-admin",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			subject := BuildSubject(tc.user, tc.privileged)
			assert.Equal(t, tc.wantCN, subject.CommonName)
			assert.Equal(t, []string{tc.wantOrg}, subject.Organization)
		})
	}
}
