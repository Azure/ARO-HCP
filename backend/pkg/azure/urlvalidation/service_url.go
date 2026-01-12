package urlvalidation

import (
	"fmt"
	"net/url"
)

// ValidateAzureServiceURL ensures the URL is parsable, with
// scheme "https", and the path is "/".
func ValidateAzureServiceURL(rawURL string) error {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("failed to parse '%s': %v", rawURL, err)
	}

	if parsedURL.Scheme != "https" {
		return fmt.Errorf("the URL is expected to be of scheme 'HTTPS'")
	}
	if parsedURL.Path != "/" {
		return fmt.Errorf("the URL is expected to be with path '/'")
	}

	return nil
}
