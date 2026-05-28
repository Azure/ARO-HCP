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

package certs

import (
	"crypto/rsa"
	"crypto/x509/pkix"

	internalcerts "github.com/Azure/ARO-HCP/internal/certs"
)

func GeneratePrivateKey(bits int) (*rsa.PrivateKey, error) {
	return internalcerts.GeneratePrivateKey(bits)
}

func GenerateCSR(privateKey *rsa.PrivateKey, subject pkix.Name) ([]byte, error) {
	return internalcerts.GenerateCSR(privateKey, subject)
}

func EncodePrivateKey(key *rsa.PrivateKey) []byte {
	return internalcerts.EncodePrivateKey(key)
}

func BuildSubject(user string, privileged bool) pkix.Name {
	return internalcerts.BuildSubject(user, privileged)
}
