package config

import "testing"

func TestGetByPath(t *testing.T) {
	tests := []struct {
		name  string
		vars  Variables
		path  string
		want  any
		found bool
	}{
		{
			name: "simple",
			vars: Variables{
				"key": "value",
			},
			path:  "key",
			want:  "value",
			found: true,
		},
		{
			name: "nested",
			vars: Variables{
				"key": Variables{
					"key": "value",
				},
			},
			path:  "key.key",
			want:  "value",
			found: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := tt.vars.GetByPath(tt.path)
			if got != tt.want {
				t.Errorf("Variables.GetByPath() got = %v, want %v", got, tt.want)
			}
			if found != tt.found {
				t.Errorf("Variables.GetByPath() found = %v, want %v", found, tt.found)
			}
		})
	}
}
