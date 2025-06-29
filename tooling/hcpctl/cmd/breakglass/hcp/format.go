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

package hcp

import (
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/breakglass"
)

// ErrorFormatter provides user-friendly error formatting for CLI output.
type ErrorFormatter struct{}

// FormatError converts errors to user-friendly messages with context and suggestions.
func (f *ErrorFormatter) FormatError(err error) string {
	if err == nil {
		return ""
	}

	var output strings.Builder

	// Handle specific error types with rich formatting
	switch e := err.(type) {
	case *breakglass.ValidationError:
		output.WriteString(fmt.Sprintf("❌ Validation Error: %s\n", e.Error()))
		if e.Field != "" && e.Value != "" {
			output.WriteString(fmt.Sprintf("   Field: %s\n", e.Field))
			output.WriteString(fmt.Sprintf("   Value: %q\n", e.Value))
		}
		output.WriteString("\n💡 Suggestions:\n")
		output.WriteString(f.getValidationSuggestions(e))

	case *breakglass.TimeoutError:
		output.WriteString(fmt.Sprintf("⏰ Timeout Error: %s\n", e.Error()))
		if e.Operation != "" {
			output.WriteString(fmt.Sprintf("   Operation: %s\n", e.Operation))
		}
		output.WriteString("\n💡 Suggestions:\n")
		output.WriteString("   • Increase the timeout with --timeout flag\n")
		output.WriteString("   • Check network connectivity\n")
		output.WriteString("   • Verify cluster is responsive\n")

	case *breakglass.ConfigurationError:
		output.WriteString(fmt.Sprintf("⚙️  Configuration Error: %s\n", e.Error()))
		if e.Component != "" {
			output.WriteString(fmt.Sprintf("   Component: %s\n", e.Component))
		}
		output.WriteString("\n💡 Suggestions:\n")
		output.WriteString("   • Check your kubeconfig file\n")
		output.WriteString("   • Verify cluster access permissions\n")
		output.WriteString("   • Ensure required configuration is present\n")

	case *breakglass.CertificateError:
		output.WriteString(fmt.Sprintf("🔐 Certificate Error: %s\n", e.Error()))
		if e.CertType != "" {
			output.WriteString(fmt.Sprintf("   Certificate Type: %s\n", e.CertType))
		}
		output.WriteString("\n💡 Suggestions:\n")
		output.WriteString("   • Check certificate expiration\n")
		output.WriteString("   • Verify certificate authority is trusted\n")
		output.WriteString("   • Ensure proper certificate chain\n")

	default:
		// Handle cluster not found (common case)
		if strings.Contains(strings.ToLower(err.Error()), "not found") &&
			strings.Contains(strings.ToLower(err.Error()), "hostedcluster") {
			output.WriteString(fmt.Sprintf("🔍 Cluster Not Found: %s\n", err.Error()))
			output.WriteString("\n💡 Suggestions:\n")
			output.WriteString("   • Verify the cluster ID is correct\n")
			output.WriteString("   • Check that the cluster exists in the management cluster\n")
			output.WriteString("   • Ensure you have access to the cluster namespace\n")
			output.WriteString("   • Confirm the cluster is properly labeled\n")
		} else {
			// Generic error formatting
			output.WriteString(fmt.Sprintf("❌ Error: %s\n", err.Error()))

			// Add general suggestions for common error patterns
			errMsg := strings.ToLower(err.Error())
			output.WriteString("\n💡 Suggestions:\n")
			switch {
			case strings.Contains(errMsg, "permission") || strings.Contains(errMsg, "unauthorized"):
				output.WriteString("   • Check your cluster access permissions\n")
				output.WriteString("   • Verify your kubeconfig is valid\n")
			case strings.Contains(errMsg, "network") || strings.Contains(errMsg, "connection"):
				output.WriteString("   • Check network connectivity\n")
				output.WriteString("   • Verify cluster endpoint is reachable\n")
			case strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline"):
				output.WriteString("   • Increase timeout with --timeout flag\n")
				output.WriteString("   • Check cluster responsiveness\n")
			default:
				output.WriteString("   • Check the error details above\n")
				output.WriteString("   • Verify your cluster configuration\n")
				output.WriteString("   • Try running with increased verbosity\n")
			}
		}
	}

	return output.String()
}

// getValidationSuggestions provides specific suggestions for validation errors.
func (f *ErrorFormatter) getValidationSuggestions(e *breakglass.ValidationError) string {
	var suggestions strings.Builder

	switch e.Field {
	case "clusterID":
		suggestions.WriteString("   • Use only alphanumeric characters and hyphens\n")
		suggestions.WriteString("   • Cannot start or end with hyphen\n")
		suggestions.WriteString("   • Maximum length is 63 characters\n")
		suggestions.WriteString("   • Example: 'my-cluster-123'\n")

	case "timeout":
		suggestions.WriteString("   • Use a duration between 1 minute and 30 days\n")
		suggestions.WriteString("   • Examples: '5m', '1h', '24h'\n")

	case "kubeconfig":
		suggestions.WriteString("   • Verify the file exists and is readable\n")
		suggestions.WriteString("   • Check the file path is correct\n")
		suggestions.WriteString("   • Ensure proper file permissions\n")

	case "user":
		suggestions.WriteString("   • Check that USER environment variable is set\n")
		suggestions.WriteString("   • Use only alphanumeric, hyphens, dots, underscores\n")
		suggestions.WriteString("   • Cannot start or end with hyphen or dot\n")

	default:
		suggestions.WriteString("   • Check the validation requirements\n")
		suggestions.WriteString("   • Verify the input format\n")
	}

	return suggestions.String()
}

// FormatErrorCompact provides a single-line error format for non-interactive use.
func (f *ErrorFormatter) FormatErrorCompact(err error) string {
	if err == nil {
		return ""
	}

	// Extract the core error message without formatting
	switch e := err.(type) {
	case *breakglass.ValidationError:
		return fmt.Sprintf("validation error: %s (field: %s)", e.Error(), e.Field)
	case *breakglass.TimeoutError:
		return fmt.Sprintf("timeout: %s", e.Error())
	case *breakglass.ConfigurationError:
		return fmt.Sprintf("configuration error: %s", e.Error())
	case *breakglass.CertificateError:
		return fmt.Sprintf("certificate error: %s", e.Error())
	default:
		return err.Error()
	}
}
