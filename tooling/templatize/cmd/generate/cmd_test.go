package generate

import (
	"bytes"
	"io"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-Tools/pkg/config"

	options "github.com/Azure/ARO-HCP/tooling/templatize/cmd"
)

func TestExecuteTemplate(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		vars  config.Variables
		input string

		expected      string
		expectedError bool
	}{
		{
			name: "happy case generates a file",
			vars: config.Variables{
				"region_maestro_keyvault":    "kv",
				"region_eventgrid_namespace": "ns",
			},
			input: `param maestroKeyVaultName = '{{ .region_maestro_keyvault }}'
param maestroEventGridNamespacesName = '{{ .region_eventgrid_namespace }}'
param maestroEventGridMaxClientSessionsPerAuthName = 4`,
			expected: `param maestroKeyVaultName = 'kv'
param maestroEventGridNamespacesName = 'ns'
param maestroEventGridMaxClientSessionsPerAuthName = 4`,
		},
		{
			name: "referencing unset variable errors",
			vars: config.Variables{
				"region_maestro_keyvault": "kv",
			},
			input: `param maestroKeyVaultName = '{{ .region_maestro_keyvault }}'
param maestroEventGridNamespacesName = '{{ .region_eventgrid_namespace }}'
param maestroEventGridMaxClientSessionsPerAuthName = 4`,
			expectedError: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			output := &bytes.Buffer{}
			opts := GenerationOptions{
				completedGenerationOptions: &completedGenerationOptions{
					InputFS:        fstest.MapFS{"test": &fstest.MapFile{Data: []byte(testCase.input)}},
					InputFile:      "test",
					OutputFile:     &nopCloser{Writer: output},
					RolloutOptions: options.NewRolloutOptions(testCase.vars),
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
