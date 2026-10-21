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

// postgres-access grants a managed identity database-level access to an Azure
// Database for PostgreSQL flexible server and registers it for Microsoft Entra
// (AAD) authentication.
//
// It replaces the postgres-access / postgres-sql Bicep deploymentScript
// modules. ARM Microsoft.Resources/deploymentScripts mount their script content
// into ACI through Azure Files, which requires shared-key storage. AME Azure
// Policy SFI-ID4.2.1 forbids shared-key storage, so the deploymentScript path is
// no longer viable. The old deploymentScript ran psql inside its own ACI image;
// the EV2 pipeline Shell runner does not ship a postgres client, so the grant is
// performed here in Go using the pure-Go lib/pq driver and an Entra access token
// (no psql / libpq required).
//
// Inputs (environment variables, set by svc-pipeline.yaml):
//
//	DEPLOY_POSTGRES   - "true" when the flex server is managed here; else skip (no-op)
//	SUBSCRIPTION_ID   - subscription holding the server and managed identity
//	RESOURCE_GROUP    - resource group holding the Postgres flexible server
//	MI_RESOURCE_GROUP - resource group holding the managed identity
//	PG_SERVER_NAME    - Postgres flexible server name
//	DATABASE_NAME     - application database to grant access to
//	NEW_USER_NAME     - managed identity (name) to grant access to
//	ADMIN_USER_NAME   - Postgres Entra admin MI name (the rollout identity)
//	PG_HOST           - optional explicit FQDN override (skips the server lookup)
//	LOG_VERBOSITY     - optional slog verbosity (default 0)
//
// Authentication mirrors the previous deploymentScript:
//   - In EV2 the Shell step runs as the rollout managed identity (shellIdentity),
//     which is the server's Entra admin -> connect as ADMIN_USER_NAME.
//   - Locally (templatize "make svc") it runs as the signed-in operator, who is
//     not the rollout MI -> enroll them as an Entra admin (idempotent) and
//     connect as their UPN, mirroring cs-current-user-pg-connect.sh.
package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers/v4"
)

// ossrdbmsScope is the Entra token scope for Azure Database for PostgreSQL,
// equivalent to `az account get-access-token --resource-type oss-rdbms`.
const ossrdbmsScope = "https://ossrdbms-aad.database.windows.net/.default"

func main() {
	verbosity := 0
	if v := os.Getenv("LOG_VERBOSITY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			verbosity = n
		}
	}
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.Level(verbosity * -1),
	})
	slog.SetDefault(slog.New(handler).With("component", "postgres-access"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := run(ctx); err != nil {
		slog.Error("run failed", "error", err.Error())
		os.Exit(1)
	}
}

// config holds all inputs sourced from environment variables.
type config struct {
	deploy          bool
	subscriptionID  string
	resourceGroup   string
	miResourceGroup string
	serverName      string
	databaseName    string
	newUserName     string
	adminUserName   string
	hostOverride    string
}

// parseEnvConfig builds a config from environment variables only. It does not
// call any external tools or APIs, which makes it safe to unit-test.
func parseEnvConfig(env func(string) string) (*config, error) {
	c := &config{
		deploy:          strings.EqualFold(env("DEPLOY_POSTGRES"), "true"),
		subscriptionID:  env("SUBSCRIPTION_ID"),
		resourceGroup:   env("RESOURCE_GROUP"),
		miResourceGroup: env("MI_RESOURCE_GROUP"),
		serverName:      env("PG_SERVER_NAME"),
		databaseName:    env("DATABASE_NAME"),
		newUserName:     env("NEW_USER_NAME"),
		adminUserName:   env("ADMIN_USER_NAME"),
		hostOverride:    env("PG_HOST"),
	}
	if !c.deploy {
		return c, nil
	}
	missing := []string{}
	for k, v := range map[string]string{
		"SUBSCRIPTION_ID":   c.subscriptionID,
		"RESOURCE_GROUP":    c.resourceGroup,
		"MI_RESOURCE_GROUP": c.miResourceGroup,
		"PG_SERVER_NAME":    c.serverName,
		"DATABASE_NAME":     c.databaseName,
		"NEW_USER_NAME":     c.newUserName,
		"ADMIN_USER_NAME":   c.adminUserName,
	} {
		if v == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return c, nil
}

func run(ctx context.Context) error {
	cfg, err := parseEnvConfig(os.Getenv)
	if err != nil {
		return err
	}
	if !cfg.deploy {
		slog.Info("DEPLOY_POSTGRES is not 'true'; Postgres is not managed in this environment (containerized DB). Skipping.")
		return nil
	}

	// DefaultAzureCredential resolves to the rollout managed identity in EV2 and
	// to the operator's `az login` locally; it never prompts interactively.
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return fmt.Errorf("azidentity: %w", err)
	}

	host := cfg.hostOverride
	if host == "" {
		host, err = lookupServerFQDN(ctx, cred, cfg)
		if err != nil {
			return fmt.Errorf("resolve server FQDN: %w", err)
		}
	}

	principalID, err := lookupIdentityPrincipalID(ctx, cred, cfg)
	if err != nil {
		return fmt.Errorf("resolve managed identity principal id: %w", err)
	}

	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{ossrdbmsScope}})
	if err != nil {
		return fmt.Errorf("acquire oss-rdbms token: %w", err)
	}

	claims := parseTokenClaims(token.Token)
	pgUser := cfg.adminUserName
	if upn := userPrincipalName(claims); upn != "" {
		// Running as a human operator (local `make svc`). Connect as the UPN
		// and ensure they are enrolled as an Entra admin so the connection is
		// authorised. This branch never runs in EV2, where the credential is a
		// managed identity with no UPN claim.
		pgUser = upn
		if err := ensureEntraAdmin(ctx, cred, cfg, claims); err != nil {
			return fmt.Errorf("enroll operator as Entra admin: %w", err)
		}
	} else if !isManagedIdentityToken(claims) {
		// Running as a service principal (Prow CI). templatize ignores
		// shellIdentity, so the token belongs to the CI SP, not the rollout
		// MSI. Enrol the SP as an Entra admin and connect as its appId.
		// MSI tokens also carry appid but are distinguished by the
		// xms_mirid claim — those take the default adminUserName path.
		appID := servicePrincipalAppID(claims)
		if appID == "" {
			return fmt.Errorf("token has no UPN, appid, or xms_mirid — cannot determine caller identity")
		}
		pgUser = appID
		if err := ensureEntraAdmin(ctx, cred, cfg, claims); err != nil {
			return fmt.Errorf("enroll CI service principal as Entra admin: %w", err)
		}
	}

	slog.Info("granting database access",
		"server", host,
		"database", cfg.databaseName,
		"identity", cfg.newUserName,
		"principalId", principalID,
		"connectAs", pgUser,
	)

	// Grants that target the cluster (role registration + database privileges)
	// run on the default `postgres` database.
	if err := execStatements(ctx, host, pgUser, "postgres", token.Token,
		roleStatements(cfg.newUserName, principalID, cfg.databaseName)); err != nil {
		return err
	}
	// Schema-level grants must run while connected to the application database.
	if err := execStatements(ctx, host, pgUser, cfg.databaseName, token.Token,
		schemaStatements(cfg.newUserName)); err != nil {
		return err
	}

	slog.Info("done")
	return nil
}

// lookupServerFQDN resolves the server's fully-qualified domain name, keeping
// the step cloud-agnostic instead of hard-coding the public-cloud suffix.
func lookupServerFQDN(ctx context.Context, cred azcore.TokenCredential, cfg *config) (string, error) {
	client, err := armpostgresqlflexibleservers.NewServersClient(cfg.subscriptionID, cred, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Get(ctx, cfg.resourceGroup, cfg.serverName, nil)
	if err != nil {
		return "", err
	}
	if resp.Properties == nil || resp.Properties.FullyQualifiedDomainName == nil {
		return "", fmt.Errorf("server %q has no fullyQualifiedDomainName", cfg.serverName)
	}
	return *resp.Properties.FullyQualifiedDomainName, nil
}

// lookupIdentityPrincipalID resolves the object (principal) id of the managed
// identity being granted access. It is a runtime value, not static config.
func lookupIdentityPrincipalID(ctx context.Context, cred azcore.TokenCredential, cfg *config) (string, error) {
	client, err := armmsi.NewUserAssignedIdentitiesClient(cfg.subscriptionID, cred, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Get(ctx, cfg.miResourceGroup, cfg.newUserName, nil)
	if err != nil {
		return "", err
	}
	if resp.Properties == nil || resp.Properties.PrincipalID == nil {
		return "", fmt.Errorf("identity %q has no principalId", cfg.newUserName)
	}
	return *resp.Properties.PrincipalID, nil
}

// ensureEntraAdmin idempotently enrols the caller as a server Entra admin.
// Used for the local-operator path (UPN) and the Prow CI path (service
// principal). EV2 runs as the rollout MSI which is already the admin.
func ensureEntraAdmin(ctx context.Context, cred azcore.TokenCredential, cfg *config, claims map[string]any) error {
	objectID, _ := claims["oid"].(string)
	tenantID, _ := claims["tid"].(string)
	if objectID == "" || tenantID == "" {
		return fmt.Errorf("token is missing oid/tid claims required for admin enrolment")
	}

	principalName := userPrincipalName(claims)
	principalType := armpostgresqlflexibleservers.PrincipalTypeUser
	if principalName == "" {
		// Service principal — use appId as the display name and set the
		// correct principal type so Entra registers it properly.
		principalName = servicePrincipalAppID(claims)
		principalType = armpostgresqlflexibleservers.PrincipalTypeServicePrincipal
	}

	client, err := armpostgresqlflexibleservers.NewAdministratorsClient(cfg.subscriptionID, cred, nil)
	if err != nil {
		return err
	}

	pager := client.NewListByServerPager(cfg.resourceGroup, cfg.serverName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, admin := range page.Value {
			if admin.Properties != nil && admin.Properties.ObjectID != nil &&
				strings.EqualFold(*admin.Properties.ObjectID, objectID) {
				slog.Info("caller already enrolled as Entra admin", "objectId", objectID)
				return nil
			}
		}
	}

	slog.Info("enrolling caller as Entra admin", "principal", principalName, "principalType", principalType)
	poller, err := client.BeginCreate(ctx, cfg.resourceGroup, cfg.serverName, objectID,
		armpostgresqlflexibleservers.ActiveDirectoryAdministratorAdd{
			Properties: &armpostgresqlflexibleservers.AdministratorPropertiesForAdd{
				PrincipalName: to.Ptr(principalName),
				PrincipalType: to.Ptr(principalType),
				TenantID:      to.Ptr(tenantID),
			},
		}, nil)
	if err != nil {
		return err
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return err
	}
	return nil
}

// roleStatements returns the idempotent statements that register the identity
// as a pgaadauth role and grant it the application database. They run on the
// `postgres` database.
func roleStatements(user, principalID, database string) []string {
	return []string{
		fmt.Sprintf(`do $$
begin
  if not exists (select * from pg_user where usename = %s) then
    create user %s;
  end if;
end
$$`, quoteLiteral(user), quoteIdent(user)),
		fmt.Sprintf(`security label for "pgaadauth" on role %s is %s`,
			quoteIdent(user), quoteLiteral(fmt.Sprintf("aadauth,oid=%s,type=service", principalID))),
		fmt.Sprintf(`grant all privileges on database %s to %s`, quoteIdent(database), quoteIdent(user)),
	}
}

// schemaStatements returns the idempotent schema-level grants. They must run
// while connected to the application database.
func schemaStatements(user string) []string {
	u := quoteIdent(user)
	return []string{
		fmt.Sprintf(`grant all on schema public to %s`, u),
		fmt.Sprintf(`grant usage on schema public to %s`, u),
		fmt.Sprintf(`grant all privileges on all tables in schema public to %s`, u),
	}
}

// execStatements opens a single connection to the given database and runs the
// statements in order, failing on the first error (equivalent to psql's
// ON_ERROR_STOP=1).
func execStatements(ctx context.Context, host, user, database, token string, statements []string) error {
	db, err := sql.Open("postgres", dsn(host, user, database, token))
	if err != nil {
		return fmt.Errorf("open %s: %w", database, err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to %s on %s as %s: %w", database, host, user, err)
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec on %s: %w\nstatement: %s", database, err, stmt)
		}
	}
	return nil
}

// dsn builds a lib/pq keyword/value connection string, escaping each value.
func dsn(host, user, database, password string) string {
	fields := [][2]string{
		{"host", host},
		{"port", "5432"},
		{"user", user},
		{"dbname", database},
		{"password", password},
		{"sslmode", "require"},
	}
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		parts = append(parts, f[0]+"="+escapeDSNValue(f[1]))
	}
	return strings.Join(parts, " ")
}

// escapeDSNValue quotes a lib/pq connection-string value when needed.
func escapeDSNValue(v string) string {
	if v == "" {
		return "''"
	}
	if !strings.ContainsAny(v, " '\\") {
		return v
	}
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
	return "'" + r.Replace(v) + "'"
}

// quoteIdent quotes a SQL identifier (double quotes, doubling embedded quotes).
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// quoteLiteral quotes a SQL string literal (single quotes, doubling embedded quotes).
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// parseTokenClaims decodes the claim set of a JWT without verifying it (the
// token was just minted by azidentity for this process). Returns an empty map
// on any parse error.
func parseTokenClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return map[string]any{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return map[string]any{}
	}
	claims := map[string]any{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return map[string]any{}
	}
	return claims
}

// userPrincipalName returns the operator UPN from a user token, or "" for a
// managed-identity / service token (which has no UPN).
func userPrincipalName(claims map[string]any) string {
	for _, key := range []string{"upn", "unique_name", "preferred_username"} {
		if v, ok := claims[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// isManagedIdentityToken returns true when the token was issued to an Azure
// Managed Identity. MSI tokens carry an "xms_mirid" claim containing the ARM
// resource ID of the identity; service-principal tokens do not.
func isManagedIdentityToken(claims map[string]any) bool {
	v, ok := claims["xms_mirid"].(string)
	return ok && v != ""
}

// servicePrincipalAppID returns the appid from a service-principal token, or
// "" for user tokens. SP and MSI tokens both carry "appid"/"azp", so callers
// must use isManagedIdentityToken first to distinguish the two.
func servicePrincipalAppID(claims map[string]any) string {
	for _, key := range []string{"appid", "azp"} {
		if v, ok := claims[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
