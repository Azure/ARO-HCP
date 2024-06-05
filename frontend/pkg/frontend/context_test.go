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
				Properties: &arm.Properties{
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
		ctx := ContextWithSubscription(context.Background(), test.sub)
		actual, err := TenantIDFromContext(ctx)
		if err != nil {
			if !test.expectErr {
				t.Errorf("expected err to be nil, got %v", err)
			}
		} else {
			if test.expectErr {
				t.Error("expected err to be non-nil")
			}

			if actual != test.expected {
				t.Errorf("expected %s, got %s", test.expected, actual)
			}
		}
	}
}
