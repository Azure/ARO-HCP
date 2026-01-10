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

package auth

import (
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

type AzureADClaims struct {
	jwt.RegisteredClaims
	AppID          string   `json:"appid"`
	AppDisplayName string   `json:"app_displayname"`
	TenantID       string   `json:"tid"`
	ObjectID       string   `json:"oid"`
	IdentityType   string   `json:"idtyp"`
	Roles          []string `json:"roles,omitempty"`
	DirectoryRoles []string `json:"wids,omitempty"`
}

// ExtractTenantIDFromToken parses a JWT token and extracts the tenant ID from the 'tid' claim.
func ExtractTenantIDFromToken(tokenString string) (string, error) {

	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, _, err := parser.ParseUnverified(tokenString, &AzureADClaims{})
	if err != nil {
		return "", fmt.Errorf("failed to parse token: %w", err)
	}

	claims, ok := token.Claims.(*AzureADClaims)
	if !ok {
		return "", fmt.Errorf("failed to cast claims to AzureADClaims")
	}

	if claims.TenantID == "" {
		return "", fmt.Errorf("token does not contain 'tid' claim")
	}

	return claims.TenantID, nil
}
