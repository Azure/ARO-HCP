package urlvalidation

import (
	"fmt"
	"net/url"
)

// ValidateAzureServiceUrl ensures the URL is parsable, with scheme "https", and the path is "/".
func ValidateAzureServiceUrl(rawUrl string) error {
	parsedUrl, err := url.ParseRequestURI(rawUrl)
	if err != nil {
		return fmt.Errorf("failed to parse '%s': %v", rawUrl, err)
	}
	if parsedUrl.Scheme != "https" {
		return fmt.Errorf("the URL is expected to be of scheme 'HTTPS'")
	}
	if parsedUrl.Path != "/" {
		return fmt.Errorf("the URL is expected to be with path '/'")
	}

	return nil
}
