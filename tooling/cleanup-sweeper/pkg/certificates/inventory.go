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

package certificates

import (
	"context"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

type InventoryOptions struct {
	IncludePending bool
	MaxItems       int
}

func (o InventoryOptions) Validate() error {
	if o.MaxItems < 0 {
		return fmt.Errorf("--max-items must not be negative")
	}
	return nil
}

type certificateInventoryClient interface {
	NewListCertificatePropertiesPager(*azcertificates.ListCertificatePropertiesOptions) *runtime.Pager[azcertificates.ListCertificatePropertiesResponse]
}

// Inventory streams active certificate metadata from the fixed DEV service vault.
func Inventory(ctx context.Context, credential azcore.TokenCredential, opts InventoryOptions, output, progress io.Writer) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	client, err := azcertificates.NewClient(VaultURL, credential, nil)
	if err != nil {
		return fmt.Errorf("create certificate client: %w", err)
	}
	return inventory(ctx, client, opts, output, progress)
}

func inventory(ctx context.Context, client certificateInventoryClient, opts InventoryOptions, output, progress io.Writer) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	writer := csv.NewWriter(output)
	if err := writer.Write([]string{
		"name",
		"id",
		"enabled",
		"created",
		"updated",
		"expires",
		"not_before",
		"recovery_level",
		"recoverable_days",
		"thumbprint",
		"tags",
	}); err != nil {
		return fmt.Errorf("write CSV header: %w", err)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush CSV header: %w", err)
	}

	pager := client.NewListCertificatePropertiesPager(&azcertificates.ListCertificatePropertiesOptions{
		IncludePending: &opts.IncludePending,
	})
	count := 0
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("list certificate metadata after %d items: %w", count, err)
		}
		for _, cert := range page.Value {
			if err := ctx.Err(); err != nil {
				return err
			}
			if opts.MaxItems > 0 && count >= opts.MaxItems {
				break
			}
			row, err := inventoryRow(cert)
			if err != nil {
				return fmt.Errorf("format certificate metadata at item %d: %w", count+1, err)
			}
			if err := writer.Write(row); err != nil {
				return fmt.Errorf("write certificate metadata at item %d: %w", count+1, err)
			}
			writer.Flush()
			if err := writer.Error(); err != nil {
				return fmt.Errorf("flush certificate metadata at item %d: %w", count+1, err)
			}
			count++
		}
		if progress != nil {
			if _, err := fmt.Fprintf(progress, "Inventoried %d active certificates\n", count); err != nil {
				return fmt.Errorf("write inventory progress: %w", err)
			}
		}
		if opts.MaxItems > 0 && count >= opts.MaxItems {
			return nil
		}
	}
	return nil
}

func inventoryRow(cert *azcertificates.CertificateProperties) ([]string, error) {
	if cert == nil {
		return nil, fmt.Errorf("certificate metadata is nil")
	}
	id := ""
	if cert.ID != nil {
		id = string(*cert.ID)
	}
	name, _, _ := certificateID(cert.ID)
	attributes := cert.Attributes
	tagsToMarshal := cert.Tags
	if tagsToMarshal == nil {
		tagsToMarshal = map[string]*string{}
	}
	tags, err := json.Marshal(tagsToMarshal)
	if err != nil {
		return nil, fmt.Errorf("marshal tags for %q: %w", name, err)
	}
	return []string{
		name,
		id,
		formatBool(attribute(attributes, func(a *azcertificates.CertificateAttributes) *bool { return a.Enabled })),
		formatTime(attribute(attributes, func(a *azcertificates.CertificateAttributes) *time.Time { return a.Created })),
		formatTime(attribute(attributes, func(a *azcertificates.CertificateAttributes) *time.Time { return a.Updated })),
		formatTime(attribute(attributes, func(a *azcertificates.CertificateAttributes) *time.Time { return a.Expires })),
		formatTime(attribute(attributes, func(a *azcertificates.CertificateAttributes) *time.Time { return a.NotBefore })),
		formatString(attribute(attributes, func(a *azcertificates.CertificateAttributes) *string { return a.RecoveryLevel })),
		formatInt32(attribute(attributes, func(a *azcertificates.CertificateAttributes) *int32 { return a.RecoverableDays })),
		strings.ToUpper(hex.EncodeToString(cert.X509Thumbprint)),
		string(tags),
	}, nil
}

func attribute[T any](attributes *azcertificates.CertificateAttributes, get func(*azcertificates.CertificateAttributes) *T) *T {
	if attributes == nil {
		return nil
	}
	return get(attributes)
}

func formatBool(value *bool) string {
	if value == nil {
		return ""
	}
	return strconv.FormatBool(*value)
}

func formatInt32(value *int32) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(int64(*value), 10)
}

func formatString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func formatTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
