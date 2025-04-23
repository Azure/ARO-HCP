package arm


import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCloudErrorBody_String(t *testing.T) {
	tests := []struct {
		name     string
		body     *CloudErrorBody
		expected string
	}{
		{
			name: "One detail",
			body: &CloudErrorBody{
				Code:    "code",
				Message: "message",
				Target:  "target",
				Details: []CloudErrorBody{
					{
						Code:    "innercode",
						Message: "innermessage",
						Target:  "innertarget",
						Details: []CloudErrorBody{},
					},
				},
			},
			expected: "code: target: message Details: innercode: innertarget: innermessage",
		},
		{
			name: "Two details",
			body: &CloudErrorBody{
				Code:    "code",
				Message: "message",
				Target:  "target",
				Details: []CloudErrorBody{
					{
						Code:    "innercode",
						Message: "innermessage",
						Target:  "innertarget",
						Details: []CloudErrorBody{},
					},
					{
						Code:    "innercode2",
						Message: "innermessage2",
						Target:  "innertarget2",
						Details: []CloudErrorBody{},
					},
				},
			},
			expected: "code: target: message Details: innercode: innertarget: innermessage, innercode2: innertarget2: innermessage2",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, test.body.String())
		})
	}
}
