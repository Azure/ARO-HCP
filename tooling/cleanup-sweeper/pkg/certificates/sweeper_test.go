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
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

var referenceTime = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

func oldCertificate(name string) *azcertificates.CertificateProperties {
	return &azcertificates.CertificateProperties{
		ID: to.Ptr(azcertificates.ID(VaultURL + "/certificates/" + name)),
		Attributes: &azcertificates.CertificateAttributes{
			Created: to.Ptr(referenceTime.Add(-30 * 24 * time.Hour)),
			Updated: to.Ptr(referenceTime.Add(-8 * 24 * time.Hour)),
		},
		X509Thumbprint: []byte{1, 2, 3},
	}
}

type fakeCertificates struct {
	pages     [][]*azcertificates.CertificateProperties
	listError int // 1-based page number
	gets      []string
	deletes   []string
	get       func(string) (azcertificates.GetCertificateResponse, error)
	delete    func(string) error
}

func (f *fakeCertificates) NewListCertificatePropertiesPager(*azcertificates.ListCertificatePropertiesOptions) *runtime.Pager[azcertificates.ListCertificatePropertiesResponse] {
	i := 0
	return runtime.NewPager(runtime.PagingHandler[azcertificates.ListCertificatePropertiesResponse]{
		More: func(azcertificates.ListCertificatePropertiesResponse) bool { return i < len(f.pages) },
		Fetcher: func(context.Context, *azcertificates.ListCertificatePropertiesResponse) (azcertificates.ListCertificatePropertiesResponse, error) {
			i++
			if i == f.listError {
				return azcertificates.ListCertificatePropertiesResponse{}, errors.New("certificate page failed")
			}
			return azcertificates.ListCertificatePropertiesResponse{CertificatePropertiesListResult: azcertificates.CertificatePropertiesListResult{Value: f.pages[i-1]}}, nil
		},
	})
}

func (f *fakeCertificates) GetCertificate(_ context.Context, name, version string, _ *azcertificates.GetCertificateOptions) (azcertificates.GetCertificateResponse, error) {
	if version != "" {
		panic("must request latest, not a historical version")
	}
	f.gets = append(f.gets, name)
	if f.get != nil {
		return f.get(name)
	}
	return latestCertificate(name), nil
}

func latestCertificate(name string) azcertificates.GetCertificateResponse {
	c := oldCertificate(name)
	return azcertificates.GetCertificateResponse{Certificate: azcertificates.Certificate{
		ID: to.Ptr(azcertificates.ID(string(*c.ID) + "/version1")), Attributes: c.Attributes, X509Thumbprint: c.X509Thumbprint,
	}}
}

func (f *fakeCertificates) DeleteCertificate(_ context.Context, name string, _ *azcertificates.DeleteCertificateOptions) (azcertificates.DeleteCertificateResponse, error) {
	f.deletes = append(f.deletes, name)
	var err error
	if f.delete != nil {
		err = f.delete(name)
	}
	return azcertificates.DeleteCertificateResponse{}, err
}

type fakeGroups struct {
	pages     [][]string
	listError int
	lists     int
	onList    func(*fakeGroups)
}

func (f *fakeGroups) NewListPager(*armresources.ResourceGroupsClientListOptions) *runtime.Pager[armresources.ResourceGroupsClientListResponse] {
	f.lists++
	if f.onList != nil {
		f.onList(f)
	}
	i := 0
	return runtime.NewPager(runtime.PagingHandler[armresources.ResourceGroupsClientListResponse]{
		More: func(armresources.ResourceGroupsClientListResponse) bool { return i < len(f.pages) },
		Fetcher: func(context.Context, *armresources.ResourceGroupsClientListResponse) (armresources.ResourceGroupsClientListResponse, error) {
			i++
			if i == f.listError {
				return armresources.ResourceGroupsClientListResponse{}, errors.New("resource group page failed")
			}
			var groups []*armresources.ResourceGroup
			if len(f.pages) > 0 {
				for _, name := range f.pages[i-1] {
					groups = append(groups, &armresources.ResourceGroup{Name: to.Ptr(name), Properties: &armresources.ResourceGroupProperties{ProvisioningState: to.Ptr("Deleting")}})
				}
			}
			return armresources.ResourceGroupsClientListResponse{ResourceGroupListResult: armresources.ResourceGroupListResult{Value: groups}}, nil
		},
	})
}

func newTestSweeper(names ...string) (*sweeper, *fakeCertificates, *fakeGroups, *fakeGroups) {
	f := &fakeCertificates{pages: [][]*azcertificates.CertificateProperties{{}}}
	for _, name := range names {
		f.pages[0] = append(f.pages[0], oldCertificate(name))
	}
	a, b := &fakeGroups{}, &fakeGroups{}
	return &sweeper{certificates: f, groups: map[string]resourceGroupClient{
		devInfrastructureSubscription: a, devSharedInfrastructureSubscription: b,
	}, now: func() time.Time { return referenceTime }}, f, a, b
}

func options(dryRun bool) Options {
	return Options{DryRun: dryRun, MinAge: 168 * time.Hour, MaxDeletions: 1000}
}

func TestEligibility(t *testing.T) {
	for _, name := range []string{"frontend-cert-prow-j1234567", "admin-api-cert-ci00-j1234567", "sessiongate-cert-ci01-j1234567", "maestro-server-j1234567", "frontend-cert-ci00-j0000100"} {
		if _, _, reason := eligible(oldCertificate(name), referenceTime, referenceTime.Add(-168*time.Hour)); reason != "" {
			t.Errorf("%s: %s", name, reason)
		}
	}
	for _, name := range []string{"frontend-cert-dev-j1234567", "frontend-cert-pers-j1234567", "frontend-cert-cspr-j1234567", "frontend-cert-mocked-j1234567", "frontend-cert-pool-j1234567", "maestro-server-prow-j1234567", "frontend-cert-ci00-j123456", "frontend-cert-ci00-j12345678", "frontend-cert-ci00-j1234567-extra", "FRONTEND-cert-ci00-j1234567", "frontend-cert-ci00-j0000000", "frontend-cert-ci00-j0000099", "maestro-server-j7654321"} {
		if _, _, reason := eligible(oldCertificate(name), referenceTime, referenceTime.Add(-168*time.Hour)); reason == "" {
			t.Errorf("accepted %s", name)
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*azcertificates.CertificateProperties)
		reason string
	}{
		{"missing attributes", func(c *azcertificates.CertificateProperties) { c.Attributes = nil }, "missing-timestamp"},
		{"missing created", func(c *azcertificates.CertificateProperties) { c.Attributes.Created = nil }, "missing-timestamp"},
		{"missing updated", func(c *azcertificates.CertificateProperties) { c.Attributes.Updated = nil }, "missing-timestamp"},
		{"zero timestamp", func(c *azcertificates.CertificateProperties) { c.Attributes.Created = to.Ptr(time.Time{}) }, "missing-timestamp"},
		{"renewed", func(c *azcertificates.CertificateProperties) {
			c.Attributes.Created = to.Ptr(referenceTime.Add(-time.Hour))
		}, "recent"},
		{"recent update", func(c *azcertificates.CertificateProperties) {
			c.Attributes.Updated = to.Ptr(referenceTime.Add(-time.Hour))
		}, "recent"},
		{"exact cutoff", func(c *azcertificates.CertificateProperties) {
			c.Attributes.Updated = to.Ptr(referenceTime.Add(-168 * time.Hour))
		}, "recent"},
		{"future created", func(c *azcertificates.CertificateProperties) {
			c.Attributes.Created = to.Ptr(referenceTime.Add(time.Hour))
		}, "future-timestamp"},
		{"future updated", func(c *azcertificates.CertificateProperties) {
			c.Attributes.Updated = to.Ptr(referenceTime.Add(time.Hour))
		}, "future-timestamp"},
		{"disabled", func(c *azcertificates.CertificateProperties) { c.Attributes.Enabled = to.Ptr(false) }, ""},
		{"persist", func(c *azcertificates.CertificateProperties) { c.Tags = map[string]*string{"persist": to.Ptr("true")} }, "protected-tag"},
		{"doNotDelete", func(c *azcertificates.CertificateProperties) {
			c.Tags = map[string]*string{"DONOTDELETE": to.Ptr(" True ")}
		}, "protected-tag"},
		{"nil tag", func(c *azcertificates.CertificateProperties) { c.Tags = map[string]*string{"persist": nil} }, ""},
		{"missing ID", func(c *azcertificates.CertificateProperties) { c.ID = nil }, "invalid-id"},
		{"other vault", func(c *azcertificates.CertificateProperties) {
			c.ID = to.Ptr(azcertificates.ID("https://other.vault.azure.net/certificates/maestro-server-j1234567"))
		}, "invalid-id"},
		{"malformed ID", func(c *azcertificates.CertificateProperties) { c.ID = to.Ptr(azcertificates.ID("%invalid")) }, "invalid-id"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cert := oldCertificate("frontend-cert-prow-j1234567")
			test.mutate(cert)
			_, _, reason := eligible(cert, referenceTime, referenceTime.Add(-168*time.Hour))
			if reason != test.reason {
				t.Fatalf("reason = %q, want %q", reason, test.reason)
			}
		})
	}
	if _, _, reason := eligible(nil, referenceTime, referenceTime); reason != "invalid-id" {
		t.Fatalf("nil certificate reason = %q", reason)
	}
}

func TestDryRunInventoryAndLimit(t *testing.T) {
	s, f, a, b := newTestSweeper("frontend-cert-prow-j1234567")
	f.pages = append(f.pages, []*azcertificates.CertificateProperties{oldCertificate("maestro-server-j2345678"), oldCertificate("admin-api-cert-ci00-j3456789")})
	b.pages = [][]string{{"unrelated"}, {"unexpected.J1234567_suffix"}}
	var logs bytes.Buffer
	ctx := logr.NewContext(t.Context(), logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil)))
	opts := options(true)
	opts.MaxDeletions = 1
	if err := s.run(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if len(f.gets) != 0 || len(f.deletes) != 0 {
		t.Fatalf("dry run accessed certificates or wrote: %+v", f)
	}
	if a.lists != 1 || b.lists != 1 {
		t.Fatalf("must inventory both subscriptions: %d, %d", a.lists, b.lists)
	}
	for _, expected := range []string{`"Scanned":3`, `"Eligible":2`, `"Selected":1`, `"live-owner":1`, `"limit":1`} {
		if !strings.Contains(logs.String(), expected) {
			t.Errorf("missing %s in %s", expected, logs.String())
		}
	}
	if strings.Count(logs.String(), "Selected CI certificate") != 1 {
		t.Fatalf("expected exactly one candidate log: %s", logs.String())
	}
}

func TestInventoryFailsClosed(t *testing.T) {
	for _, test := range []string{"certificate later page", "first subscription later page", "second subscription later page", "missing subscription", "missing RG name", "duplicate certificate", "refresh error", "slow inventory"} {
		t.Run(test, func(t *testing.T) {
			s, f, a, b := newTestSweeper("maestro-server-j1234567")
			switch test {
			case "certificate later page":
				f.pages = append(f.pages, nil)
				f.listError = 2
			case "first subscription later page":
				a.pages = [][]string{{"other"}, {}}
				a.listError = 2
			case "second subscription later page":
				b.pages = [][]string{{"other"}, {}}
				b.listError = 2
			case "missing subscription":
				delete(s.groups, devSharedInfrastructureSubscription)
			case "missing RG name":
				b.pages = [][]string{{""}}
			case "duplicate certificate":
				f.pages = append(f.pages, f.pages[0])
			case "refresh error":
				b.onList = func(f *fakeGroups) {
					if f.lists == 2 {
						f.listError = 1
					}
				}
			case "slow inventory":
				b.onList = func(*fakeGroups) { s.now = func() time.Time { return referenceTime.Add(30 * time.Second) } }
			}
			if err := s.run(t.Context(), options(false)); err == nil {
				t.Fatal("expected closed failure")
			}
			if len(f.deletes) != 0 {
				t.Fatalf("deleted despite incomplete guard: %v", f.deletes)
			}
		})
	}
}

func TestOwnerTokens(t *testing.T) {
	s, _, a, b := newTestSweeper()
	a.pages = [][]string{{"ci00-j1234567-rg", "J2345678", "other_j3456789.j4567890"}}
	b.pages = [][]string{{"xj5678901", "j67890123", "j7890123x", "j123456-j8901234"}}
	owners, _, err := s.owners(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"j1234567": true, "j2345678": true, "j3456789": true, "j4567890": true, "j8901234": true}
	if !reflect.DeepEqual(owners, want) {
		t.Fatalf("owners = %v, want %v", owners, want)
	}
}

func TestApplyRevalidation(t *testing.T) {
	for _, test := range []struct {
		name    string
		mutate  func(*azcertificates.GetCertificateResponse)
		deleted bool
	}{
		{name: "unchanged", deleted: true},
		{name: "renewed", mutate: func(c *azcertificates.GetCertificateResponse) {
			c.Attributes.Created = to.Ptr(referenceTime.Add(-time.Hour))
		}},
		{name: "old but changed created", mutate: func(c *azcertificates.GetCertificateResponse) {
			c.Attributes.Created = to.Ptr(referenceTime.Add(-20 * 24 * time.Hour))
		}},
		{name: "old but changed updated", mutate: func(c *azcertificates.GetCertificateResponse) {
			c.Attributes.Updated = to.Ptr(referenceTime.Add(-10 * 24 * time.Hour))
		}},
		{name: "future", mutate: func(c *azcertificates.GetCertificateResponse) {
			c.Attributes.Updated = to.Ptr(referenceTime.Add(time.Hour))
		}},
		{name: "missing attrs", mutate: func(c *azcertificates.GetCertificateResponse) { c.Attributes = nil }},
		{name: "missing created", mutate: func(c *azcertificates.GetCertificateResponse) { c.Attributes.Created = nil }},
		{name: "missing updated", mutate: func(c *azcertificates.GetCertificateResponse) { c.Attributes.Updated = nil }},
		{name: "new persistent tag", mutate: func(c *azcertificates.GetCertificateResponse) { c.Tags = map[string]*string{"persist": to.Ptr("true")} }},
		{name: "new doNotDelete tag", mutate: func(c *azcertificates.GetCertificateResponse) {
			c.Tags = map[string]*string{"doNotDelete": to.Ptr("true")}
		}},
		{name: "new thumbprint", mutate: func(c *azcertificates.GetCertificateResponse) { c.X509Thumbprint = []byte{4} }},
		{name: "unversioned GET", mutate: func(c *azcertificates.GetCertificateResponse) { c.ID = oldCertificate("maestro-server-j1234567").ID }},
		{name: "wrong name", mutate: func(c *azcertificates.GetCertificateResponse) {
			c.ID = to.Ptr(azcertificates.ID(VaultURL + "/certificates/maestro-server-j2345678/version1"))
		}},
		{name: "version changed", mutate: func(c *azcertificates.GetCertificateResponse) {
			c.ID = to.Ptr(azcertificates.ID(VaultURL + "/certificates/maestro-server-j1234567/version2"))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, f, a, b := newTestSweeper("maestro-server-j1234567")
			if test.name == "version changed" {
				f.pages[0][0].ID = to.Ptr(azcertificates.ID(VaultURL + "/certificates/maestro-server-j1234567/version1"))
			}
			f.get = func(name string) (azcertificates.GetCertificateResponse, error) {
				c := latestCertificate(name)
				if test.mutate != nil {
					test.mutate(&c)
				}
				return c, nil
			}
			if err := s.run(t.Context(), options(false)); err != nil {
				t.Fatal(err)
			}
			if (len(f.deletes) == 1) != test.deleted {
				t.Fatalf("deletes = %v, want deletion %t", f.deletes, test.deleted)
			}
			if a.lists != 2 || b.lists != 2 {
				t.Fatalf("both owner guards must refresh before first delete: %d, %d", a.lists, b.lists)
			}
		})
	}
}

func TestOwnerRefresh(t *testing.T) {
	for _, when := range []string{"before first delete", "after 30 seconds", "refresh failure after delete", "slow certificate GET", "refresh failure after slow certificate GET"} {
		t.Run(when, func(t *testing.T) {
			s, f, _, b := newTestSweeper("maestro-server-j1234567", "frontend-cert-prow-j2345678")
			clock := referenceTime
			s.now = func() time.Time { return clock }
			b.onList = func(g *fakeGroups) {
				if when == "before first delete" && g.lists == 2 {
					g.pages = [][]string{{"any-j1234567"}, {"any-j2345678"}}
				}
				if g.lists == 3 {
					if when == "after 30 seconds" || when == "refresh failure after delete" {
						g.pages = [][]string{{"any-j2345678"}}
					}
					if when == "refresh failure after delete" {
						g.listError = 1
					}
					if when == "refresh failure after slow certificate GET" {
						g.listError = 1
					}
				}
			}
			f.delete = func(string) error { clock = clock.Add(30 * time.Second); return nil }
			if strings.Contains(when, "slow certificate GET") {
				gets := 0
				f.get = func(name string) (azcertificates.GetCertificateResponse, error) {
					gets++
					if gets == 1 {
						clock = clock.Add(30 * time.Second)
					}
					return latestCertificate(name), nil
				}
			}
			err := s.run(t.Context(), options(false))
			wantError := when == "refresh failure after delete" || when == "refresh failure after slow certificate GET"
			if (err != nil) != wantError {
				t.Fatalf("error = %v, want error %t", err, wantError)
			}
			wantDeletes := 1
			if when == "before first delete" || when == "refresh failure after slow certificate GET" {
				wantDeletes = 0
			}
			if len(f.deletes) != wantDeletes {
				t.Fatalf("deletes = %v, want %d", f.deletes, wantDeletes)
			}
			if when == "slow certificate GET" && f.deletes[0] != "frontend-cert-prow-j2345678" {
				t.Fatalf("did not defer the certificate with stale owner inventory: %v", f.deletes)
			}
		})
	}
}

func TestFailuresAndBoundedAttempts(t *testing.T) {
	for _, operation := range []string{"delete failure", "GET failure", "delete 404", "GET 404", "success"} {
		t.Run(operation, func(t *testing.T) {
			s, f, _, _ := newTestSweeper("maestro-server-j1234567", "maestro-server-j2345678", "maestro-server-j3456789")
			opts := options(false)
			opts.MaxDeletions = 2
			if strings.HasPrefix(operation, "delete") {
				f.delete = func(string) error {
					if operation == "delete 404" {
						return &azcore.ResponseError{StatusCode: 404}
					}
					return errors.New("delete failed")
				}
			}
			if strings.HasPrefix(operation, "GET") {
				f.get = func(string) (azcertificates.GetCertificateResponse, error) {
					if operation == "GET 404" {
						return azcertificates.GetCertificateResponse{}, &azcore.ResponseError{StatusCode: 404}
					}
					return azcertificates.GetCertificateResponse{}, errors.New("GET failed")
				}
			}
			err := s.run(t.Context(), opts)
			if (err != nil) != strings.HasSuffix(operation, "failure") {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(f.gets) != 2 {
				t.Fatalf("must process exactly selected limit, even on failures: %v", f.gets)
			}
			wantDeletes := 2
			if strings.HasPrefix(operation, "GET") {
				wantDeletes = 0
			}
			if len(f.deletes) != wantDeletes {
				t.Fatalf("deletes = %v, want %d", f.deletes, wantDeletes)
			}
			if err != nil && !strings.Contains(err.Error(), "j2345678") {
				t.Fatalf("failure was not aggregated: %v", err)
			}
		})
	}
}

func TestCancellation(t *testing.T) {
	s, f, _, _ := newTestSweeper("maestro-server-j1234567")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.get = func(name string) (azcertificates.GetCertificateResponse, error) {
		cancel()
		return latestCertificate(name), nil
	}
	if err := s.run(ctx, options(false)); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if len(f.deletes) != 0 {
		t.Fatalf("deleted after cancellation: %v", f.deletes)
	}
}

func TestOptions(t *testing.T) {
	for _, opts := range []Options{{MinAge: 23 * time.Hour, MaxDeletions: 1}, {MinAge: 168 * time.Hour}, {MinAge: 168 * time.Hour, MaxDeletions: -1}} {
		if opts.Validate() == nil {
			t.Fatalf("accepted unsafe options: %+v", opts)
		}
	}
	if err := (Options{MinAge: 24 * time.Hour, MaxDeletions: 1}).Validate(); err != nil {
		t.Fatal(err)
	}
}
