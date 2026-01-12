// Copyright 2026 Microsoft Corporation
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
