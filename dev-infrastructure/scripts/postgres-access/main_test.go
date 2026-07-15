// Copyright 2026 Microsoft Corporation
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

package main

import (
	"strings"
	"testing"
)

func TestParseEnvConfig(t *testing.T) {
	t.Run("skip when not deploying", func(t *testing.T) {
		c, err := parseEnvConfig(func(string) string { return "" })
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.deploy {
			t.Fatalf("expected deploy=false")
		}
	})

	t.Run("missing required when deploying", func(t *testing.T) {
		_, err := parseEnvConfig(func(k string) string {
			if k == "DEPLOY_POSTGRES" {
				return "true"
			}
			return ""
		})
		if err == nil {
			t.Fatalf("expected error for missing required vars")
		}
		for _, want := range []string{"SUBSCRIPTION_ID", "PG_SERVER_NAME", "NEW_USER_NAME"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q should mention %q", err.Error(), want)
			}
		}
	})

	t.Run("complete config", func(t *testing.T) {
		env := map[string]string{
			"DEPLOY_POSTGRES":   "TRUE",
			"SUBSCRIPTION_ID":   "sub",
			"RESOURCE_GROUP":    "rg",
			"MI_RESOURCE_GROUP": "mirg",
			"PG_SERVER_NAME":    "srv",
			"DATABASE_NAME":     "clusters-service",
			"NEW_USER_NAME":     "clusters-service",
			"ADMIN_USER_NAME":   "global-rollout-identity",
		}
		c, err := parseEnvConfig(func(k string) string { return env[k] })
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !c.deploy || c.databaseName != "clusters-service" || c.adminUserName != "global-rollout-identity" {
			t.Fatalf("unexpected config: %+v", c)
		}
	})
}

func TestRoleStatements(t *testing.T) {
	stmts := roleStatements("clusters-service", "1daf5200-78ea-454d-84a1-a15f98876f5d", "clusters-service")
	joined := strings.Join(stmts, "\n")

	// Hyphenated identifiers must be double-quoted or Postgres rejects them.
	if !strings.Contains(joined, `create user "clusters-service"`) {
		t.Errorf("create user must quote the hyphenated identifier: %s", joined)
	}
	if !strings.Contains(joined, `grant all privileges on database "clusters-service" to "clusters-service"`) {
		t.Errorf("database grant must quote identifiers: %s", joined)
	}
	if !strings.Contains(joined, `oid=1daf5200-78ea-454d-84a1-a15f98876f5d,type=service`) {
		t.Errorf("security label must embed the principal oid: %s", joined)
	}
	if !strings.Contains(joined, `on role "clusters-service"`) {
		t.Errorf("security label must target the quoted role: %s", joined)
	}
}

func TestSchemaStatements(t *testing.T) {
	stmts := schemaStatements("maestro-server")
	if len(stmts) != 3 {
		t.Fatalf("expected 3 schema statements, got %d", len(stmts))
	}
	for _, s := range stmts {
		if !strings.Contains(s, `"maestro-server"`) {
			t.Errorf("schema statement must quote the identifier: %s", s)
		}
	}
}

func TestQuoteIdent(t *testing.T) {
	cases := map[string]string{
		"clusters-service": `"clusters-service"`,
		"maestro":          `"maestro"`,
		`we"ird`:           `"we""ird"`,
	}
	for in, want := range cases {
		if got := quoteIdent(in); got != want {
			t.Errorf("quoteIdent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQuoteLiteral(t *testing.T) {
	if got := quoteLiteral("a'b"); got != "'a''b'" {
		t.Errorf("quoteLiteral = %q", got)
	}
}

func TestEscapeDSNValue(t *testing.T) {
	cases := map[string]string{
		"simple":     "simple",
		"":           "''",
		"with space": "'with space'",
		`back\slash`: `'back\\slash'`,
		"quote'd":    `'quote\'d'`,
	}
	for in, want := range cases {
		if got := escapeDSNValue(in); got != want {
			t.Errorf("escapeDSNValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDSN(t *testing.T) {
	got := dsn("srv.postgres.database.azure.com", "user@x", "postgres", "tok en")
	for _, want := range []string{
		"host=srv.postgres.database.azure.com",
		"port=5432",
		"user=user@x",
		"dbname=postgres",
		"sslmode=require",
		`password='tok en'`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dsn missing %q in %q", want, got)
		}
	}
}

func TestUserPrincipalName(t *testing.T) {
	if got := userPrincipalName(map[string]any{"upn": "rael@redhat.com"}); got != "rael@redhat.com" {
		t.Errorf("upn = %q", got)
	}
	if got := userPrincipalName(map[string]any{"unique_name": "u@x"}); got != "u@x" {
		t.Errorf("unique_name = %q", got)
	}
	// Managed-identity / service token has no UPN.
	if got := userPrincipalName(map[string]any{"appid": "abc", "idtyp": "app"}); got != "" {
		t.Errorf("service token should have empty UPN, got %q", got)
	}
}

func TestIsManagedIdentityToken(t *testing.T) {
	// MSI token has xms_mirid claim.
	if !isManagedIdentityToken(map[string]any{"xms_mirid": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/my-mi", "appid": "abc", "oid": "o"}) {
		t.Error("expected MSI token to be detected")
	}
	// SP token has no xms_mirid.
	if isManagedIdentityToken(map[string]any{"appid": "abc", "oid": "o"}) {
		t.Error("SP token should not be detected as MSI")
	}
	// User token has no xms_mirid.
	if isManagedIdentityToken(map[string]any{"upn": "user@x", "oid": "o"}) {
		t.Error("user token should not be detected as MSI")
	}
}

func TestServicePrincipalAppID(t *testing.T) {
	// SP token with appid claim (v1 token).
	if got := servicePrincipalAppID(map[string]any{"appid": "abc-123", "oid": "o"}); got != "abc-123" {
		t.Errorf("appid = %q, want abc-123", got)
	}
	// SP token with azp claim (v2 token).
	if got := servicePrincipalAppID(map[string]any{"azp": "def-456"}); got != "def-456" {
		t.Errorf("azp = %q, want def-456", got)
	}
	// appid takes precedence over azp when both present.
	if got := servicePrincipalAppID(map[string]any{"appid": "first", "azp": "second"}); got != "first" {
		t.Errorf("expected appid precedence, got %q", got)
	}
	// User token has no appid/azp.
	if got := servicePrincipalAppID(map[string]any{"upn": "user@x", "oid": "o"}); got != "" {
		t.Errorf("user token should have empty appid, got %q", got)
	}
}

func TestParseTokenClaims(t *testing.T) {
	// header.payload.signature with payload {"upn":"u@x","oid":"o","tid":"t"}
	const tok = "aaa.eyJ1cG4iOiJ1QHgiLCJvaWQiOiJvIiwidGlkIjoidCJ9.bbb"
	claims := parseTokenClaims(tok)
	if claims["upn"] != "u@x" || claims["oid"] != "o" || claims["tid"] != "t" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if len(parseTokenClaims("not-a-jwt")) != 0 {
		t.Errorf("malformed token should yield empty claims")
	}
}
