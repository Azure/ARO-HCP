package certs

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
)

func GeneratePrivateKey(bits int) (*rsa.PrivateKey, error) {
	return generatePrivateKeyWithReader(rand.Reader, bits)
}

func generatePrivateKeyWithReader(reader io.Reader, bits int) (*rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(reader, bits)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}
	return key, nil
}

func GenerateCSR(privateKey *rsa.PrivateKey, subject pkix.Name) ([]byte, error) {
	return generateCSRWithRngSource(rand.Reader, privateKey, subject)
}

func generateCSRWithRngSource(rngSource io.Reader, privateKey *rsa.PrivateKey, subject pkix.Name) ([]byte, error) {
	template := x509.CertificateRequest{
		Subject:            subject,
		SignatureAlgorithm: x509.SHA256WithRSA,
	}

	csrDER, err := x509.CreateCertificateRequest(rngSource, &template, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate request: %w", err)
	}

	csrPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrDER,
	})

	return csrPEM, nil
}

func EncodePrivateKey(key *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

func BuildSubject(user string, privileged bool) pkix.Name {
	organization := "aro-sre"
	if privileged {
		organization = "aro-sre-cluster-admin"
	}

	return pkix.Name{
		CommonName:   fmt.Sprintf("system:sre-break-glass:%s", user),
		Organization: []string{organization},
	}
}
