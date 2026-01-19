package validation

import (
	"context"
	"fmt"
	"net/url"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// ValidateAzureServiceURL ensures the URL is parsable, with
// scheme "https", and the path is "/".
func ValidateAzureServiceURL(_ context.Context, _ operation.Operation, fldPath *field.Path, rawURL string) field.ErrorList {
	errs := field.ErrorList{}
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		errs = append(errs, field.Invalid(fldPath, rawURL, fmt.Sprintf("attribute is not a valid azure service url: %v", err)))
		return errs
	}

	if parsedURL.Scheme != "https" {
		errs = append(errs, field.Invalid(fldPath, rawURL, "the URL is expected to be of scheme 'HTTPS'"))
	}

	if parsedURL.Path != "/" {
		errs = append(errs, field.Invalid(fldPath, rawURL, "the URL is expected to be with path '/'"))
	}

	return errs
}
