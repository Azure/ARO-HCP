package frontend

import (
	"context"
	"testing"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func stringPtr(s string) *string {
	return &s
}

func TestTenantIDFromContext(t *testing.T) {
	tests := []struct {
		name      string
		sub       arm.Subscription
		expected  string
		expectErr bool
	}{
		{
			name: "Valid",
			sub: arm.Subscription{
				Properties: &arm.SubscriptionProperties{
					TenantId: stringPtr("tenant-id"),
				},
			},
			expected:  "tenant-id",
			expectErr: false,
		},
		{
			name:      "Missing tenantId",
			sub:       arm.Subscription{},
			expectErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := ContextWithSubscription(context.Background(), test.sub)
			actual, err := TenantIDFromContext(ctx)
			assertError(t, actual, test.expected, err, test.expectErr)
		})
	}
}

func assertError(t *testing.T, actual string, expected string, err error, expectErr bool) {
	if err != nil {
		if !expectErr {
			t.Errorf("expected err to be nil, got %v", err)
		}
		return
	}

	if expectErr {
		t.Error("expected err to be non-nil")
	}

	if actual != expected {
		t.Errorf("expected %s, got %s", expected, actual)
	}
}
