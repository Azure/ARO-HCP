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
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

func TestInventoryStreamsAllPages(t *testing.T) {
	s, f, _, _ := newTestSweeper()
	first := oldCertificate("maestro-server-j1234567")
	first.Attributes.Enabled = to.Ptr(true)
	first.Attributes.Expires = to.Ptr(referenceTime.Add(180 * 24 * time.Hour))
	first.Attributes.NotBefore = to.Ptr(referenceTime.Add(-30 * 24 * time.Hour))
	first.Attributes.RecoveryLevel = to.Ptr("Recoverable+Purgeable")
	first.Attributes.RecoverableDays = to.Ptr(int32(90))
	first.Tags = map[string]*string{"comma": to.Ptr("one,two"), "nil": nil}
	second := oldCertificate("admin-api-cert-ci00-j2345678")
	f.pages = [][]*azcertificates.CertificateProperties{{first}, {second}}

	var output, progress bytes.Buffer
	if err := inventory(t.Context(), s.certificates, InventoryOptions{IncludePending: true}, &output, &progress); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(output.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want header and two certificates\n%s", len(rows), output.String())
	}
	if rows[1][0] != "maestro-server-j1234567" ||
		rows[1][2] != "true" ||
		rows[1][3] != first.Attributes.Created.UTC().Format(time.RFC3339) ||
		rows[1][7] != "Recoverable+Purgeable" ||
		rows[1][8] != "90" ||
		rows[1][9] != "010203" ||
		rows[1][10] != `{"comma":"one,two","nil":null}` {
		t.Fatalf("unexpected first row: %#v", rows[1])
	}
	if !strings.Contains(progress.String(), "Inventoried 1 active certificates") ||
		!strings.Contains(progress.String(), "Inventoried 2 active certificates") {
		t.Fatalf("unexpected progress: %q", progress.String())
	}
	if f.activePagesRead != 2 {
		t.Fatalf("pages read = %d, want 2", f.activePagesRead)
	}
}

func TestInventoryMaxItems(t *testing.T) {
	s, f, _, _ := newTestSweeper()
	f.pages = [][]*azcertificates.CertificateProperties{{
		oldCertificate("maestro-server-j1234567"),
		oldCertificate("maestro-server-j2345678"),
	}, {
		oldCertificate("maestro-server-j3456789"),
	}}
	var output, progress bytes.Buffer
	if err := inventory(t.Context(), s.certificates, InventoryOptions{MaxItems: 1}, &output, &progress); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(output.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1][0] != "maestro-server-j1234567" {
		t.Fatalf("unexpected rows: %#v", rows)
	}
	if f.activePagesRead != 1 {
		t.Fatalf("pages read = %d, want 1", f.activePagesRead)
	}
	if !strings.Contains(progress.String(), "Inventoried 1 active certificates") {
		t.Fatalf("unexpected progress: %q", progress.String())
	}
}

func TestInventoryErrors(t *testing.T) {
	t.Run("invalid options", func(t *testing.T) {
		if err := (InventoryOptions{MaxItems: -1}).Validate(); err == nil {
			t.Fatal("accepted negative max items")
		}
	})
	t.Run("page", func(t *testing.T) {
		s, f, _, _ := newTestSweeper()
		f.listError = 1
		if err := inventory(t.Context(), s.certificates, InventoryOptions{}, &bytes.Buffer{}, nil); err == nil || !strings.Contains(err.Error(), "after 0 items") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("nil certificate", func(t *testing.T) {
		s, f, _, _ := newTestSweeper()
		f.pages = [][]*azcertificates.CertificateProperties{{nil}}
		if err := inventory(t.Context(), s.certificates, InventoryOptions{}, &bytes.Buffer{}, nil); err == nil || !strings.Contains(err.Error(), "metadata is nil") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		s, f, _, _ := newTestSweeper()
		f.pages = [][]*azcertificates.CertificateProperties{{oldCertificate("maestro-server-j1234567")}}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := inventory(ctx, s.certificates, InventoryOptions{}, &bytes.Buffer{}, nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestInventoryRowMissingAttributes(t *testing.T) {
	row, err := inventoryRow(&azcertificates.CertificateProperties{
		ID: to.Ptr(azcertificates.ID(VaultURL + "/certificates/test")),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"test", VaultURL + "/certificates/test", "", "", "", "", "", "", "", "", "{}"}
	if !reflect.DeepEqual(row, want) {
		t.Fatalf("row = %#v, want %#v", row, want)
	}
}
