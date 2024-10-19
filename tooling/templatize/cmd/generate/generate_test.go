package generate

import (
	"bytes"
	"io"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-HCP/tooling/templatize/internal/config"
)

func TestExecuteTemplate(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		config config.Variables
		input  string

		expected      string
		expectedError bool
	}{
		{
			name: "happy case generates a file",
			config: config.Variables{
				"region_maestro_keyvault":    "kv",
				"region_eventgrid_namespace": "ns",
			},
			input: `param maestroKeyVaultName = '{{index . "region_maestro_keyvault"}}'
param maestroEventGridNamespacesName = '{{index . "region_eventgrid_namespace"}}'
param maestroEventGridMaxClientSessionsPerAuthName = 4`,
			expected: `param maestroKeyVaultName = 'kv'
param maestroEventGridNamespacesName = 'ns'
param maestroEventGridMaxClientSessionsPerAuthName = 4`,
		},
		{
			name: "referencing unset variable errors", // TODO: this does not error today, just gets an empty string, this is not the UX we want
			config: config.Variables{
				"region_maestro_keyvault": "kv",
			},
			input: `param maestroKeyVaultName = '{{index . "region_maestro_keyvault"}}'
param maestroEventGridNamespacesName = '{{index . "region_eventgrid_namespace"}}'
param maestroEventGridMaxClientSessionsPerAuthName = 4`,
			expected: `param maestroKeyVaultName = 'kv'
param maestroEventGridNamespacesName = ''
param maestroEventGridMaxClientSessionsPerAuthName = 4`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			output := &bytes.Buffer{}
			opts := GenerationOptions{
				completedGenerationOptions: &completedGenerationOptions{
					Config:    testCase.config,
					Input:     fstest.MapFS{"test": &fstest.MapFile{Data: []byte(testCase.input)}},
					InputFile: "test",
					Output:    &nopCloser{Writer: output},
				},
			}
			err := opts.ExecuteTemplate()
			if testCase.expectedError {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			if diff := cmp.Diff(output.String(), testCase.expected); diff != "" {
				t.Errorf("unexpected output (-want, +got): %s", diff)
			}
		})
	}
}

type nopCloser struct {
	io.Writer
}

func (n nopCloser) Close() error {
	return nil
}

var _ io.WriteCloser = &nopCloser{}
