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

//go:build integration

package util

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const skipNoAuthMsg = "No authentication available - set AZURE_TENANT_ID or ALLOW_AZ_CLI_FALLBACK=true"

// TestResourceTracker tracks resources created during tests for cleanup
type TestResourceTracker struct {
	mu      sync.Mutex
	apps    []string // Application IDs
	groups  []string // Group IDs
	cleanup bool
}

// NewTestResourceTracker creates a new resource tracker
func NewTestResourceTracker() *TestResourceTracker {
	return &TestResourceTracker{
		cleanup: true,
	}
}

// AddApplication adds an application ID to the tracker
func (tr *TestResourceTracker) AddApplication(appID string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.apps = append(tr.apps, appID)
}

// AddGroup adds a group ID to the tracker
func (tr *TestResourceTracker) AddGroup(groupID string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.groups = append(tr.groups, groupID)
}

// DisableCleanup disables automatic cleanup (useful for debugging)
func (tr *TestResourceTracker) DisableCleanup() {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.cleanup = false
}

// CleanupAll performs cleanup of all tracked resources
func (tr *TestResourceTracker) CleanupAll(ctx context.Context, client *Client, t *testing.T) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	if isDryRun() {
		t.Logf("DRY-RUN: Skipping cleanup of %d apps and %d groups", len(tr.apps), len(tr.groups))
		t.Logf("DRY-RUN:     apps: %v", tr.apps)
		t.Logf("DRY-RUN:     groups: %v", tr.groups)
		return
	}

	if !tr.cleanup {
		t.Logf("Cleanup disabled - resources remain: %d apps, %d groups", len(tr.apps), len(tr.groups))
		return
	}

	// Clean up applications
	for _, appID := range tr.apps {
		if err := client.DeleteApplication(ctx, appID); err != nil {
			t.Logf("Warning: Failed to cleanup application %s: %v", appID, err)
		} else {
			t.Logf("Cleaned up application %s", appID)
		}
	}

	// Note: Groups can't be deleted via the current SDK, so we just log them
	for _, groupID := range tr.groups {
		t.Logf("Warning: Group %s requires manual cleanup - not supported by SDK", groupID)
	}
}

// Safety checks and configuration
func requireIntegrationTestSafety(t *testing.T) {
	// 1. Require explicit consent via environment variable
	if os.Getenv("INTEGRATION_TEST_CONSENT") != "true" {
		t.Skip("Integration tests require explicit consent - set INTEGRATION_TEST_CONSENT=true")
	}

	// 2. Require a test-specific tenant ID to prevent accidental production use
	testTenantID := os.Getenv("TEST_AZURE_TENANT_ID")
	if testTenantID == "" {
		t.Skip("Integration tests require TEST_AZURE_TENANT_ID to prevent production use")
	}

	// 3. Require a test-specific resource prefix to identify test resources
	if os.Getenv("TEST_RESOURCE_PREFIX") == "" {
		t.Skip("Integration tests require TEST_RESOURCE_PREFIX to identify test resources")
	}

	// 4. Notify for dry-run mode
	if isDryRun() {
		t.Logf("DRY-RUN MODE: Simulating operations without modifying resources")
	}

	// 5. Validate we're not in a production environment
	tenantID := os.Getenv("AZURE_TENANT_ID")
	if tenantID != testTenantID {
		t.Fatalf("AZURE_TENANT_ID (%s) must match TEST_AZURE_TENANT_ID (%s) for safety", tenantID, testTenantID)
	}
}

// isDryRun checks if dry-run mode is enabled
func isDryRun() bool {
	return os.Getenv("INTEGRATION_TEST_DRY_RUN") == "true"
}

// generateTestName creates a unique test name with proper prefixing
func generateTestName(prefix string) string {
	testPrefix := os.Getenv("TEST_RESOURCE_PREFIX")
	if testPrefix == "" {
		testPrefix = "test"
	}
	timestamp := time.Now().Format("20060102150405")
	return fmt.Sprintf("%s-%s-%s", testPrefix, prefix, timestamp)
}

// TestIntegration_ClientCreation tests that we can create a client with proper authentication
func TestIntegration_ClientCreation(t *testing.T) {
	requireIntegrationTestSafety(t)

	// Skip if no authentication is available
	if !hasAuthentication() {
		t.Skip(skipNoAuthMsg)
	}

	ctx := context.Background()
	client, err := NewClient(ctx)
	require.NoError(t, err)
	require.NotNil(t, client)

	// Test that we can get the tenant ID
	tenantID := client.GetTenantID()
	assert.NotEmpty(t, tenantID)
	assert.True(t, len(tenantID) > 0, "Tenant ID should not be empty")

	// Test that we can get the underlying Graph client
	graphClient := client.GetGraphClient()
	assert.NotNil(t, graphClient)
}

// TestIntegration_OrganizationOperations tests organization-related operations
func TestIntegration_OrganizationOperations(t *testing.T) {
	requireIntegrationTestSafety(t)

	// Skip if no authentication is available
	if !hasAuthentication() {
		t.Skip(skipNoAuthMsg)
	}

	ctx := context.Background()
	client, err := NewClient(ctx)
	require.NoError(t, err)

	// Test getting organization
	org, err := client.GetOrganization(ctx)
	require.NoError(t, err)
	assert.NotNil(t, org)
	assert.NotEmpty(t, org.ID)
	assert.NotEmpty(t, org.DisplayName)
	assert.Equal(t, client.GetTenantID(), org.ID, "Organization ID should match tenant ID")

}

// TestIntegration_UserOperations tests user-related operations
func TestIntegration_UserOperations(t *testing.T) {
	requireIntegrationTestSafety(t)

	// Skip if no authentication is available
	if !hasAuthentication() {
		t.Skip(skipNoAuthMsg)
	}

	ctx := context.Background()
	client, err := NewClient(ctx)
	require.NoError(t, err)

	// Test getting current user
	user, err := client.GetCurrentUser(ctx)
	require.NoError(t, err)
	assert.NotNil(t, user)
	assert.NotEmpty(t, user.ID)
	assert.NotEmpty(t, user.DisplayName)
	assert.NotEmpty(t, user.UserPrincipalName)

	// Verify email format if present
	if user.Mail != "" {
		assert.Contains(t, user.Mail, "@", "Email should contain @ symbol")
	}
}

// TestIntegration_ApplicationOperations tests application-related operations
func TestIntegration_ApplicationOperations(t *testing.T) {
	requireIntegrationTestSafety(t)

	// Skip if no authentication is available
	if !hasAuthentication() {
		t.Skip(skipNoAuthMsg)
	}

	// Create a test application with unique name
	displayName := generateTestName("app")

	ctx := context.Background()
	client, err := NewClient(ctx)
	require.NoError(t, err)

	if isDryRun() {
		t.Logf("DRY-RUN: Skipping application CREATE: %s", displayName)
	} else {
		// Create resource tracker for cleanup
		tracker := NewTestResourceTracker()
		defer tracker.CleanupAll(ctx, client, t)

		app, err := client.CreateApplication(ctx, displayName, "AzureADMyOrg", []string{})
		require.NoError(t, err)
		assert.NotNil(t, app)
		assert.NotEmpty(t, app.ID)
		assert.NotEmpty(t, app.AppID)
		assert.Equal(t, displayName, app.DisplayName)

		// Track the application for cleanup
		tracker.AddApplication(app.ID)

		// Test getting the application
		retrievedApp, err := client.GetApplication(ctx, app.ID)
		require.NoError(t, err)
		assert.Equal(t, app.ID, retrievedApp.ID)
		assert.Equal(t, app.AppID, retrievedApp.AppID)
		assert.Equal(t, app.DisplayName, retrievedApp.DisplayName)

		// Test adding a password
		startTime := time.Now().UTC().Round(time.Second)
		endTime := startTime.Add(24 * time.Hour)
		passwordCred, err := client.AddPassword(ctx, app.ID, "test-secret", startTime, endTime)
		require.NoError(t, err)
		assert.NotNil(t, passwordCred)
		assert.NotEmpty(t, passwordCred.SecretText)
		assert.NotEmpty(t, passwordCred.KeyID)
		assert.Equal(t, startTime, passwordCred.StartTime)
		assert.Equal(t, endTime, passwordCred.EndTime)

		// Test updating redirect URIs
		redirectURIs := []string{"https://localhost:3000/callback"}
		err = client.UpdateApplicationRedirectUris(ctx, app.ID, redirectURIs)
		require.NoError(t, err)

		// Verify the application still exists after updates
		updatedApp, err := client.GetApplication(ctx, app.ID)
		require.NoError(t, err)
		assert.Equal(t, app.ID, updatedApp.ID)
	}
}

// TestIntegration_GroupOperations tests group-related operations
func TestIntegration_GroupOperations(t *testing.T) {
	requireIntegrationTestSafety(t)

	// Skip if no authentication is available
	if !hasAuthentication() {
		t.Skip(skipNoAuthMsg)
	}

	// Create a test security group with unique name
	displayName := generateTestName("group")
	description := "Test security group for integration testing"

	ctx := context.Background()
	client, err := NewClient(ctx)
	require.NoError(t, err)

	// Create resource tracker for cleanup
	tracker := NewTestResourceTracker()
	if !isDryRun() {
		defer tracker.CleanupAll(ctx, client, t)
	}

	if isDryRun() {
		t.Logf("DRY-RUN: Skipping group CREATE: %s", displayName)
	} else {
		group, err := client.CreateSecurityGroup(ctx, displayName, description)
		require.NoError(t, err)
		assert.NotNil(t, group)
		assert.NotEmpty(t, group.ID)
		assert.Equal(t, displayName, group.DisplayName)
		assert.Equal(t, description, group.Description)

		// Track the group for cleanup
		tracker.AddGroup(group.ID)

		// Note: We can't test DeleteGroup or GetGroup as they're not implemented in the SDK
		// The group will remain in the tenant and should be cleaned up manually if needed
		t.Logf("Created test group %s with ID %s - manual cleanup may be required", displayName, group.ID)
	}
}

// TestIntegration_ErrorHandling tests error handling scenarios
func TestIntegration_ErrorHandling(t *testing.T) {
	requireIntegrationTestSafety(t)

	// Skip if no authentication is available
	if !hasAuthentication() {
		t.Skip(skipNoAuthMsg)
	}

	ctx := context.Background()
	client, err := NewClient(ctx)
	require.NoError(t, err)

	// Test getting non-existent application
	_, err = client.GetApplication(ctx, "non-existent-id")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// Test deleting non-existent application
	if !isDryRun() {
		err = client.DeleteApplication(ctx, "non-existent-id")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	} else {
		t.Logf("DRY-RUN: Skipping application DELETE: non-existent-id")
	}
}

// TestIntegration_ConcurrentOperations tests concurrent operations
func TestIntegration_ConcurrentOperations(t *testing.T) {
	requireIntegrationTestSafety(t)

	// Skip if no authentication is available
	if !hasAuthentication() {
		t.Skip(skipNoAuthMsg)
	}

	ctx := context.Background()
	client, err := NewClient(ctx)
	require.NoError(t, err)

	// Test concurrent organization and user operations
	orgChan := make(chan *Organization, 1)
	userChan := make(chan *User, 1)
	errChan := make(chan error, 2)

	go func() {
		org, err := client.GetOrganization(ctx)
		if err != nil {
			errChan <- err
			return
		}
		orgChan <- org
	}()

	go func() {
		user, err := client.GetCurrentUser(ctx)
		if err != nil {
			errChan <- err
			return
		}
		userChan <- user
	}()

	// Wait for both operations to complete
	org := <-orgChan
	user := <-userChan

	// Check for errors
	select {
	case err := <-errChan:
		t.Fatalf("Concurrent operation failed: %v", err)
	default:
		// No errors
	}

	assert.NotNil(t, org)
	assert.NotNil(t, user)
	assert.NotEmpty(t, org.ID)
	assert.NotEmpty(t, user.ID)
}

// Helper function to check if authentication is available
func hasAuthentication() bool {
	// Check for client secret authentication
	if os.Getenv("AZURE_TENANT_ID") != "" &&
		os.Getenv("AZURE_CLIENT_ID") != "" &&
		os.Getenv("AZURE_CLIENT_SECRET") != "" {
		return true
	}

	// Check for Azure CLI fallback
	if os.Getenv("ALLOW_AZ_CLI_FALLBACK") == "true" {
		return true
	}

	return false
}
