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
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	cleanuppolicy "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/policy"
)

const testVaultURL = "https://aro-hcp-dev-svc-kv.vault.azure.net"

var referenceTime = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

func oldCertificate(name string) *azcertificates.CertificateProperties {
	return &azcertificates.CertificateProperties{
		ID: to.Ptr(azcertificates.ID(testVaultURL + "/certificates/" + name)),
		Attributes: &azcertificates.CertificateAttributes{
			Created: to.Ptr(referenceTime.Add(-30 * 24 * time.Hour)),
			Updated: to.Ptr(referenceTime.Add(-8 * 24 * time.Hour)),
		},
		X509Thumbprint: []byte{1, 2, 3},
	}
}

func deletedCertificate(name string) *azcertificates.DeletedCertificateProperties {
	return &azcertificates.DeletedCertificateProperties{
		ID:                 to.Ptr(azcertificates.ID(testVaultURL + "/certificates/" + name + "/version1")),
		RecoveryID:         to.Ptr(testVaultURL + "/deletedcertificates/" + name),
		Attributes:         &azcertificates.CertificateAttributes{RecoveryLevel: to.Ptr("Recoverable+Purgeable")},
		DeletedDate:        to.Ptr(referenceTime.Add(-time.Hour)),
		ScheduledPurgeDate: to.Ptr(referenceTime.Add(89 * 24 * time.Hour)),
		X509Thumbprint:     []byte{1, 2, 3},
	}
}

type fakeCertificates struct {
	mu               sync.Mutex
	pages            [][]*azcertificates.CertificateProperties
	listError        int // 1-based page number
	deletedPages     [][]*azcertificates.DeletedCertificateProperties
	deletedListError int // 1-based page number
	activeLists      int
	activePagesRead  int
	deletedLists     int
	deletedPagesRead int
	gets             []string
	deletes          []string
	deletedGets      []string
	purges           []string
	get              func(string) (azcertificates.GetCertificateResponse, error)
	delete           func(string) error
	getDeleted       func(string) (azcertificates.GetDeletedCertificateResponse, error)
	purge            func(string) error
}

func (f *fakeCertificates) NewListCertificatePropertiesPager(*azcertificates.ListCertificatePropertiesOptions) *runtime.Pager[azcertificates.ListCertificatePropertiesResponse] {
	f.activeLists++
	i := 0
	return runtime.NewPager(runtime.PagingHandler[azcertificates.ListCertificatePropertiesResponse]{
		More: func(azcertificates.ListCertificatePropertiesResponse) bool { return i < len(f.pages) },
		Fetcher: func(context.Context, *azcertificates.ListCertificatePropertiesResponse) (azcertificates.ListCertificatePropertiesResponse, error) {
			i++
			f.activePagesRead++
			if i == f.listError {
				return azcertificates.ListCertificatePropertiesResponse{}, errors.New("certificate page failed")
			}
			return azcertificates.ListCertificatePropertiesResponse{CertificatePropertiesListResult: azcertificates.CertificatePropertiesListResult{Value: f.pages[i-1]}}, nil
		},
	})
}

func (f *fakeCertificates) NewListDeletedCertificatePropertiesPager(*azcertificates.ListDeletedCertificatePropertiesOptions) *runtime.Pager[azcertificates.ListDeletedCertificatePropertiesResponse] {
	f.deletedLists++
	i := 0
	return runtime.NewPager(runtime.PagingHandler[azcertificates.ListDeletedCertificatePropertiesResponse]{
		More: func(azcertificates.ListDeletedCertificatePropertiesResponse) bool { return i < len(f.deletedPages) },
		Fetcher: func(context.Context, *azcertificates.ListDeletedCertificatePropertiesResponse) (azcertificates.ListDeletedCertificatePropertiesResponse, error) {
			i++
			f.deletedPagesRead++
			if i == f.deletedListError {
				return azcertificates.ListDeletedCertificatePropertiesResponse{}, errors.New("deleted certificate page failed")
			}
			return azcertificates.ListDeletedCertificatePropertiesResponse{DeletedCertificatePropertiesListResult: azcertificates.DeletedCertificatePropertiesListResult{Value: f.deletedPages[i-1]}}, nil
		},
	})
}

func (f *fakeCertificates) GetCertificate(_ context.Context, name, version string, _ *azcertificates.GetCertificateOptions) (azcertificates.GetCertificateResponse, error) {
	if version != "" {
		panic("must request latest, not a historical version")
	}
	f.mu.Lock()
	f.gets = append(f.gets, name)
	f.mu.Unlock()
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
	f.mu.Lock()
	f.deletes = append(f.deletes, name)
	f.mu.Unlock()
	var err error
	if f.delete != nil {
		err = f.delete(name)
	}
	return azcertificates.DeleteCertificateResponse{}, err
}

func (f *fakeCertificates) GetDeletedCertificate(_ context.Context, name string, _ *azcertificates.GetDeletedCertificateOptions) (azcertificates.GetDeletedCertificateResponse, error) {
	f.mu.Lock()
	f.deletedGets = append(f.deletedGets, name)
	f.mu.Unlock()
	if f.getDeleted != nil {
		return f.getDeleted(name)
	}
	c := deletedCertificate(name)
	return azcertificates.GetDeletedCertificateResponse{DeletedCertificate: azcertificates.DeletedCertificate{
		Attributes:         c.Attributes,
		ID:                 c.ID,
		RecoveryID:         c.RecoveryID,
		Tags:               c.Tags,
		X509Thumbprint:     c.X509Thumbprint,
		DeletedDate:        c.DeletedDate,
		ScheduledPurgeDate: c.ScheduledPurgeDate,
	}}, nil
}

func (f *fakeCertificates) PurgeDeletedCertificate(_ context.Context, name string, _ *azcertificates.PurgeDeletedCertificateOptions) (azcertificates.PurgeDeletedCertificateResponse, error) {
	f.mu.Lock()
	f.purges = append(f.purges, name)
	f.mu.Unlock()
	var err error
	if f.purge != nil {
		err = f.purge(name)
	}
	return azcertificates.PurgeDeletedCertificateResponse{}, err
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

const (
	testSubscriptionA = "subscription-a"
	testSubscriptionB = "subscription-b"
)

// testSweeper wires a Session to fakes. Fields of the embedded Session may be
// tweaked per test before run.
type testSweeper struct {
	*Session
	groups map[string]ResourceGroupLister
}

type testOptions struct {
	DryRun       bool
	DeleteActive bool
	PurgeDeleted bool
	MaxDeletions int
	MaxPurges    int
	Workers      int
	Policy       *cleanuppolicy.CertificatePolicy
}

func newTestSweeper(names ...string) (*testSweeper, *fakeCertificates, *fakeGroups, *fakeGroups) {
	f := &fakeCertificates{
		pages:        [][]*azcertificates.CertificateProperties{{}},
		deletedPages: [][]*azcertificates.DeletedCertificateProperties{{}},
	}
	for _, name := range names {
		f.pages[0] = append(f.pages[0], oldCertificate(name))
	}
	a, b := &fakeGroups{}, &fakeGroups{}
	s := &testSweeper{groups: map[string]ResourceGroupLister{
		testSubscriptionA: a, testSubscriptionB: b,
	}}
	s.Session = &Session{
		client:    f,
		vaultURL:  testVaultURL,
		vaultHost: strings.TrimPrefix(testVaultURL, "https://"),
		now:       func() time.Time { return referenceTime },
		counts:    newSummary(),
	}
	s.guard = &ownerGuard{
		owners: NewOwnerInventory([]string{testSubscriptionA, testSubscriptionB}, s.groups, func() time.Time { return s.now() }),
		now:    func() time.Time { return s.now() },
	}
	s.policy = repositoryPolicyForTests().Discovery
	return s, f, a, b
}

func sweeperOnly() *testSweeper {
	s, _, _, _ := newTestSweeper()
	return s
}

var cachedPolicy *cleanuppolicy.CertificatePolicy

func repositoryPolicyForTests() *cleanuppolicy.CertificatePolicy {
	if cachedPolicy == nil {
		loaded, err := cleanuppolicy.LoadCertificates("../../../../certificates.policy.yaml")
		if err != nil {
			panic(err)
		}
		cachedPolicy = loaded
	}
	return cachedPolicy
}

func options(dryRun bool) testOptions {
	return testOptions{
		DryRun:       dryRun,
		DeleteActive: true,
		PurgeDeleted: true,
		MaxDeletions: 1000,
		MaxPurges:    1000,
		Workers:      1,
	}
}

// run applies opts to the session and executes the workflow exactly as the
// engine workflow builder assembles it.
func (s *testSweeper) run(ctx context.Context, opts testOptions) error {
	if _, err := logr.FromContext(ctx); err != nil {
		ctx = logr.NewContext(ctx, logr.Discard())
	}
	if opts.Policy != nil {
		s.policy = opts.Policy.Discovery
	}
	s.dryRun, s.deleteActive, s.purgeDeleted = opts.DryRun, opts.DeleteActive, opts.PurgeDeleted
	s.maxDeletions, s.maxPurges = opts.MaxDeletions, opts.MaxPurges
	engine := &runner.Engine{
		Steps:       s.Steps(),
		Parallelism: opts.Workers,
		DryRun:      opts.DryRun,
		Wait:        true,
		PostRunFn:   s.Finish,
	}
	return engine.Run(ctx)
}

func TestEligibility(t *testing.T) {
	for _, name := range []string{"frontend-cert-prow-j1234567", "admin-api-cert-ci00-j1234567", "sessiongate-cert-ci01-j1234567", "maestro-server-j1234567", "frontend-cert-ci00-j0000100"} {
		if _, _, reason := sweeperOnly().eligible(oldCertificate(name), nil, referenceTime); !reason.Selected() {
			t.Errorf("%s: %s", name, reason)
		}

	}
	for _, name := range []string{"frontend-cert-dev-j1234567", "frontend-cert-cspr-j1234567", "frontend-cert-mocked-j1234567", "frontend-cert-pool-j1234567", "maestro-server-ci01-j1234567", "frontend-cert-ci00-j123456", "frontend-cert-ci00-j12345678", "frontend-cert-ci00-j1234567-extra", "FRONTEND-cert-ci00-j1234567", "frontend-cert-ci00-j0000000", "frontend-cert-ci00-j0000099", "maestro-server-j7654321"} {
		if _, _, reason := sweeperOnly().eligible(oldCertificate(name), nil, referenceTime); reason.Selected() {
			t.Errorf("accepted %s", name)
		}
	}

	for _, test := range []struct {
		name   string
		mutate func(*azcertificates.CertificateProperties)
		reason cleanuppolicy.CertificateDecision
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
		{"disabled", func(c *azcertificates.CertificateProperties) { c.Attributes.Enabled = to.Ptr(false) }, cleanuppolicy.CertificateDecisionSelected},
		{"persist", func(c *azcertificates.CertificateProperties) { c.Tags = map[string]*string{"persist": to.Ptr("true")} }, "protected-tag"},
		{"persist key and value are case-insensitive", func(c *azcertificates.CertificateProperties) {
			c.Tags = map[string]*string{"PERSIST": to.Ptr(" True ")}
		}, "protected-tag"},
		{"unlisted tag is not protective", func(c *azcertificates.CertificateProperties) {
			c.Tags = map[string]*string{"doNotDelete": to.Ptr("true")}
		}, cleanuppolicy.CertificateDecisionSelected},
		{"nil tag", func(c *azcertificates.CertificateProperties) { c.Tags = map[string]*string{"persist": nil} }, cleanuppolicy.CertificateDecisionSelected},
		{"missing ID", func(c *azcertificates.CertificateProperties) { c.ID = nil }, "invalid-id"},
		{"other vault", func(c *azcertificates.CertificateProperties) {
			c.ID = to.Ptr(azcertificates.ID("https://other.vault.azure.net/certificates/maestro-server-j1234567"))
		}, "invalid-id"},
		{"malformed ID", func(c *azcertificates.CertificateProperties) { c.ID = to.Ptr(azcertificates.ID("%invalid")) }, "invalid-id"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cert := oldCertificate("frontend-cert-prow-j1234567")
			test.mutate(cert)
			_, _, reason := sweeperOnly().eligible(cert, nil, referenceTime)
			if reason != test.reason {
				t.Fatalf("reason = %q, want %q", reason, test.reason)
			}
		})
	}
	if _, _, reason := sweeperOnly().eligible(nil, nil, referenceTime); reason != "invalid-id" {
		t.Fatalf("nil certificate reason = %q", reason)
	}
}

func TestDeletedEligibility(t *testing.T) {
	for _, name := range []string{"frontend-cert-prow-j1234567", "admin-api-cert-ci00-j1234567", "sessiongate-cert-ci01-j1234567", "maestro-server-j1234567"} {
		if _, _, reason := sweeperOnly().eligibleDeleted(deletedCertificate(name)); !reason.Selected() {
			t.Errorf("%s: %s", name, reason)
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*azcertificates.DeletedCertificateProperties)
		reason cleanuppolicy.CertificateDecision
	}{
		{"wrong name", func(c *azcertificates.DeletedCertificateProperties) {
			c.ID = deletedCertificate("frontend-cert-dev-j1234567").ID
			c.RecoveryID = to.Ptr(testVaultURL + "/deletedcertificates/frontend-cert-dev-j1234567")
		}, "no-rule-match"},
		{"placeholder", func(c *azcertificates.DeletedCertificateProperties) {
			c.ID = deletedCertificate("maestro-server-j0000001").ID
			c.RecoveryID = to.Ptr(testVaultURL + "/deletedcertificates/maestro-server-j0000001")
		}, cleanuppolicy.CertificateDecisionSkippedByRule},
		{"protected tag", func(c *azcertificates.DeletedCertificateProperties) {
			c.Tags = map[string]*string{"persist": to.Ptr("true")}
		}, "protected-tag"},
		{"missing recovery ID", func(c *azcertificates.DeletedCertificateProperties) { c.RecoveryID = nil }, "invalid-recovery-id"},
		{"wrong recovery name", func(c *azcertificates.DeletedCertificateProperties) {
			c.RecoveryID = to.Ptr(testVaultURL + "/deletedcertificates/maestro-server-j2345678")
		}, "invalid-recovery-id"},
		{"other recovery vault", func(c *azcertificates.DeletedCertificateProperties) {
			c.RecoveryID = to.Ptr("https://other.vault.azure.net/deletedcertificates/maestro-server-j1234567")
		}, "invalid-recovery-id"},
		{"missing attributes", func(c *azcertificates.DeletedCertificateProperties) { c.Attributes = nil }, "not-purgeable"},
		{"not purgeable", func(c *azcertificates.DeletedCertificateProperties) {
			c.Attributes.RecoveryLevel = to.Ptr("Recoverable")
		}, "not-purgeable"},
		{"missing deleted date", func(c *azcertificates.DeletedCertificateProperties) { c.DeletedDate = nil }, "missing-deletion-timestamp"},
		{"missing scheduled purge", func(c *azcertificates.DeletedCertificateProperties) { c.ScheduledPurgeDate = nil }, "missing-deletion-timestamp"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cert := deletedCertificate("maestro-server-j1234567")
			test.mutate(cert)
			_, _, reason := sweeperOnly().eligibleDeleted(cert)
			if reason != test.reason {
				t.Fatalf("reason = %q, want %q", reason, test.reason)
			}
		})
	}
	if _, _, reason := sweeperOnly().eligibleDeleted(nil); reason != "invalid-id" {
		t.Fatalf("nil deleted certificate reason = %q", reason)
	}
}

func TestDryRunInventoryAndLimit(t *testing.T) {
	s, f, a, b := newTestSweeper("frontend-cert-prow-j1234567")
	f.pages[0] = append(f.pages[0], oldCertificate("maestro-server-j2345678"))
	f.pages = append(f.pages, []*azcertificates.CertificateProperties{oldCertificate("admin-api-cert-ci00-j3456789")})
	f.listError = 2
	f.deletedPages = [][]*azcertificates.DeletedCertificateProperties{{
		deletedCertificate("sessiongate-cert-ci01-j4567890"),
		deletedCertificate("maestro-server-j5678901"),
	}}
	b.pages = [][]string{{"unrelated"}, {"unexpected.J1234567_suffix"}}
	var logs bytes.Buffer
	ctx := logr.NewContext(t.Context(), logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil)))
	opts := options(true)
	opts.MaxDeletions = 1
	opts.MaxPurges = 1
	if err := s.run(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if len(f.gets) != 0 || len(f.deletes) != 0 || len(f.deletedGets) != 0 || len(f.purges) != 0 {
		t.Fatalf("dry run accessed certificates or wrote: %+v", f)
	}
	if a.lists != 1 || b.lists != 1 {
		t.Fatalf("must inventory both subscriptions: %d, %d", a.lists, b.lists)
	}
	for _, expected := range []string{`"Scanned":2`, `"Eligible":1`, `"Selected":1`, `"DeletedScanned":1`, `"PurgeEligible":1`, `"PurgeSelected":1`, `"live-owner":1`, `"limit":1`, `"purge-limit":1`} {
		if !strings.Contains(logs.String(), expected) {
			t.Errorf("missing %s in %s", expected, logs.String())
		}
	}
	if strings.Count(logs.String(), "Selected CI certificate") != 1 {
		t.Fatalf("expected exactly one candidate log: %s", logs.String())
	}
	if strings.Count(logs.String(), "Selected deleted CI certificate") != 1 {
		t.Fatalf("expected exactly one purge candidate log: %s", logs.String())
	}
	for _, expected := range []string{
		`"policyRule":"current-ci-job-certificates"`,
		`"reason":"rule[1]-expired"`,
		`"reason":"rule[1]-deleted"`,
	} {
		if !strings.Contains(logs.String(), expected) {
			t.Errorf("missing candidate policy detail %s in %s", expected, logs.String())
		}
	}
}

func TestMutationLimitsScanPastIneligibleCertificates(t *testing.T) {
	t.Run("active", func(t *testing.T) {
		s, f, _, _ := newTestSweeper("frontend-cert-dev-j1234567")
		f.pages = append(f.pages,
			[]*azcertificates.CertificateProperties{oldCertificate("maestro-server-j2345678")},
			nil,
		)
		f.listError = 3
		var logs bytes.Buffer
		ctx := logr.NewContext(t.Context(), logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil)))
		opts := options(true)
		opts.PurgeDeleted = false
		opts.MaxDeletions = 1
		if err := s.run(ctx, opts); err != nil {
			t.Fatal(err)
		}
		if f.activePagesRead != 2 {
			t.Fatalf("must scan past ineligible active metadata to fill mutation budget; read %d pages", f.activePagesRead)
		}
		for _, expected := range []string{`"Scanned":2`, `"Eligible":1`, `"Selected":1`, `"name":"maestro-server-j2345678"`} {
			if !strings.Contains(logs.String(), expected) {
				t.Errorf("missing %s in %s", expected, logs.String())
			}
		}
		if len(f.gets) != 0 || len(f.deletes) != 0 {
			t.Fatalf("dry run accessed certificates or wrote: gets=%v deletes=%v", f.gets, f.deletes)
		}
	})

	t.Run("deleted", func(t *testing.T) {
		s, f, _, _ := newTestSweeper()
		ineligible := deletedCertificate("frontend-cert-dev-j1234567")
		ineligible.RecoveryID = to.Ptr(testVaultURL + "/deletedcertificates/frontend-cert-dev-j1234567")
		f.deletedPages = [][]*azcertificates.DeletedCertificateProperties{
			{ineligible},
			{deletedCertificate("maestro-server-j2345678")},
			nil,
		}
		f.deletedListError = 3
		var logs bytes.Buffer
		ctx := logr.NewContext(t.Context(), logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil)))
		opts := options(true)
		opts.DeleteActive = false
		opts.MaxPurges = 1
		if err := s.run(ctx, opts); err != nil {
			t.Fatal(err)
		}
		if f.deletedPagesRead != 2 {
			t.Fatalf("must scan past ineligible deleted metadata to fill mutation budget; read %d pages", f.deletedPagesRead)
		}
		for _, expected := range []string{`"DeletedScanned":2`, `"PurgeEligible":1`, `"PurgeSelected":1`, `"name":"maestro-server-j2345678"`} {
			if !strings.Contains(logs.String(), expected) {
				t.Errorf("missing %s in %s", expected, logs.String())
			}
		}
		if len(f.deletedGets) != 0 || len(f.purges) != 0 {
			t.Fatalf("dry run accessed deleted certificates or purged: gets=%v purges=%v", f.deletedGets, f.purges)
		}
	})
}

func TestInventoryFailsClosed(t *testing.T) {
	for _, test := range []string{"certificate later page", "deleted certificate later page", "first subscription later page", "second subscription later page", "missing subscription", "missing RG name", "duplicate certificate", "duplicate deleted certificate", "refresh error", "slow inventory"} {
		t.Run(test, func(t *testing.T) {
			s, f, a, b := newTestSweeper("maestro-server-j1234567")
			switch test {
			case "certificate later page":
				f.pages = append(f.pages, nil)
				f.listError = 2
			case "deleted certificate later page":
				f.deletedPages = append(f.deletedPages, nil)
				f.deletedListError = 2
			case "first subscription later page":
				a.pages = [][]string{{"other"}, {}}
				a.listError = 2
			case "second subscription later page":
				b.pages = [][]string{{"other"}, {}}
				b.listError = 2
			case "missing subscription":
				delete(s.groups, testSubscriptionB)
			case "missing RG name":
				b.pages = [][]string{{""}}
			case "duplicate certificate":
				f.pages = append(f.pages, f.pages[0])
			case "duplicate deleted certificate":
				f.deletedPages = [][]*azcertificates.DeletedCertificateProperties{{
					deletedCertificate("maestro-server-j2345678"),
				}, {
					deletedCertificate("maestro-server-j2345678"),
				}}
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
			if strings.Contains(test, "deleted certificate") {
				// Active certificates are cleaned before tombstones are listed, so
				// only the tombstone phase is gated on its own inventory: nothing
				// listed there may be purged.
				if slices.Contains(f.purges, "maestro-server-j2345678") {
					t.Fatalf("purged a tombstone from an incomplete inventory: purges=%v", f.purges)
				}
				return
			}
			if len(f.deletes) != 0 || len(f.purges) != 0 {
				t.Fatalf("changed certificates despite incomplete inventory: deletes=%v purges=%v", f.deletes, f.purges)
			}
		})
	}
}

func TestOwnerTokens(t *testing.T) {
	s, _, a, b := newTestSweeper()
	a.pages = [][]string{{"ci00-j1234567-rg", "J2345678", "other_j3456789.j4567890"}}
	b.pages = [][]string{{"xj5678901", "j67890123", "j7890123x", "j123456-j8901234"}}
	owners, _, err := s.guard.owners(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"ci00", "j123456", "j1234567", "j2345678", "j3456789", "j4567890", "j67890123", "j7890123x", "j8901234", "other", "rg", "xj5678901"}
	if !reflect.DeepEqual(sets.List(owners), want) {
		t.Fatalf("owners = %v, want %v", owners, want)
	}
}

func TestRepositoryRecurringPolicy(t *testing.T) {
	testCases := []struct {
		name        string
		certificate string
		updated     time.Time
		ownerRG     string
		deleted     bool
	}{
		{
			name:        "deletes ownerless personal frontend certificate after retention",
			certificate: "frontend-cert-pers-usw3rvaz",
			updated:     referenceTime.Add(-16 * 24 * time.Hour),
			deleted:     true,
		},
		{
			name:        "retains personal frontend certificate with live owner",
			certificate: "frontend-cert-pers-usw3rvaz",
			updated:     referenceTime.Add(-16 * 24 * time.Hour),
			ownerRG:     "hcp-underlay-pers-usw3rvaz-svc",
		},
		{
			name:        "enforces the personal retention window",
			certificate: "frontend-cert-pers-usw3rvaz",
			updated:     referenceTime.Add(-8 * 24 * time.Hour),
		},
		{
			name:        "deletes ownerless personal Maestro certificate",
			certificate: "maestro-server-usw3rvaz",
			updated:     referenceTime.Add(-16 * 24 * time.Hour),
			deleted:     true,
		},
		{
			name:        "deletes ownerless ephemeral performance certificate",
			certificate: "frontend-cert-perf-usw3rvaz",
			updated:     referenceTime.Add(-16 * 24 * time.Hour),
			deleted:     true,
		},
		{
			name:        "retains unmatched old-schema frontend certificate",
			certificate: "frontend-cert-usw3rvaz",
			updated:     referenceTime.Add(-30 * 24 * time.Hour),
		},
		{
			name:        "retains placeholder job certificate",
			certificate: "maestro-server-j0000001",
			updated:     referenceTime.Add(-30 * 24 * time.Hour),
		},
		{
			name:        "retains fixture job certificate",
			certificate: "frontend-cert-prow-j7654321",
			updated:     referenceTime.Add(-30 * 24 * time.Hour),
		},
		{
			name:        "retains unscoped CSPR frontend certificate",
			certificate: "frontend-cert-cspr",
			updated:     referenceTime.Add(-30 * 24 * time.Hour),
		},
	}

	loadedPolicy, err := cleanuppolicy.LoadCertificates("../../../../certificates.policy.yaml")
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s, f, a, _ := newTestSweeper(tc.certificate)
			f.pages[0][0].Attributes.Updated = to.Ptr(tc.updated)
			f.get = func(name string) (azcertificates.GetCertificateResponse, error) {
				response := latestCertificate(name)
				response.Attributes.Updated = to.Ptr(tc.updated)
				return response, nil
			}
			if tc.ownerRG != "" {
				a.pages = [][]string{{tc.ownerRG}}
			}
			opts := options(false)
			opts.Policy = loadedPolicy
			opts.PurgeDeleted = false
			if err := s.run(t.Context(), opts); err != nil {
				t.Fatal(err)
			}
			if got := len(f.deletes) == 1; got != tc.deleted {
				t.Fatalf("deletes=%v, want deletion=%t", f.deletes, tc.deleted)
			}
		})
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
		{name: "new thumbprint", mutate: func(c *azcertificates.GetCertificateResponse) { c.X509Thumbprint = []byte{4} }},
		{name: "unversioned GET", mutate: func(c *azcertificates.GetCertificateResponse) { c.ID = oldCertificate("maestro-server-j1234567").ID }},
		{name: "wrong name", mutate: func(c *azcertificates.GetCertificateResponse) {
			c.ID = to.Ptr(azcertificates.ID(testVaultURL + "/certificates/maestro-server-j2345678/version1"))
		}},
		{name: "version changed", mutate: func(c *azcertificates.GetCertificateResponse) {
			c.ID = to.Ptr(azcertificates.ID(testVaultURL + "/certificates/maestro-server-j1234567/version2"))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, f, a, b := newTestSweeper("maestro-server-j1234567")
			if test.name == "version changed" {
				f.pages[0][0].ID = to.Ptr(azcertificates.ID(testVaultURL + "/certificates/maestro-server-j1234567/version1"))
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
			s, f, _, b := newTestSweeper("maestro-server-j1234567", "frontend-cert-ci01-j2345678")
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
			if when == "slow certificate GET" && f.deletes[0] != "frontend-cert-ci01-j2345678" {
				t.Fatalf("did not defer the certificate with stale owner inventory: %v", f.deletes)
			}
		})
	}
}

func TestOwnerlessPolicyDoesNotRequireOwnerInventory(t *testing.T) {
	s, f, a, b := newTestSweeper("temporary-certificate")
	s.policy = cleanuppolicy.CertificateDiscoveryPolicy{Rules: []cleanuppolicy.CertificateDiscoveryRule{{
		Name:                   "ownerless-certificate",
		AllowWithoutOwnerGuard: true,
		Action:                 cleanuppolicy.CertificateDiscoveryActionDelete,
		Match:                  cleanuppolicy.CertificateDiscoveryMatch{NameRegex: regexp.MustCompile(`^temporary-certificate$`)},
		OlderThan:              24 * time.Hour,
	}}}
	clock := referenceTime
	s.now = func() time.Time { return clock }
	f.get = func(name string) (azcertificates.GetCertificateResponse, error) {
		clock = clock.Add(ownerMaxAge)
		return latestCertificate(name), nil
	}
	opts := options(false)
	opts.Policy = &cleanuppolicy.CertificatePolicy{Discovery: s.policy}
	opts.PurgeDeleted = false
	if err := s.run(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.deletes, []string{"temporary-certificate"}) {
		t.Fatalf("deletes = %v, want temporary-certificate", f.deletes)
	}
	if a.lists != 0 || b.lists != 0 {
		t.Fatalf("owner inventory was loaded for ownerless policy: %d, %d", a.lists, b.lists)
	}
}

func TestPurgeRevalidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*azcertificates.GetDeletedCertificateResponse)
		purged bool
	}{
		{name: "unchanged", purged: true},
		{name: "protected", mutate: func(c *azcertificates.GetDeletedCertificateResponse) {
			c.Tags = map[string]*string{"persist": to.Ptr("true")}
		}},
		{name: "not purgeable", mutate: func(c *azcertificates.GetDeletedCertificateResponse) {
			c.Attributes.RecoveryLevel = to.Ptr("Recoverable")
		}},
		{name: "changed deleted date", mutate: func(c *azcertificates.GetDeletedCertificateResponse) {
			c.DeletedDate = to.Ptr(referenceTime.Add(-2 * time.Hour))
		}},
		{name: "changed purge date", mutate: func(c *azcertificates.GetDeletedCertificateResponse) {
			c.ScheduledPurgeDate = to.Ptr(referenceTime.Add(88 * 24 * time.Hour))
		}},
		{name: "wrong recovery ID", mutate: func(c *azcertificates.GetDeletedCertificateResponse) {
			c.RecoveryID = to.Ptr(testVaultURL + "/deletedcertificates/maestro-server-j2345678")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, f, _, _ := newTestSweeper()
			f.deletedPages = [][]*azcertificates.DeletedCertificateProperties{{deletedCertificate("maestro-server-j1234567")}}
			f.getDeleted = func(name string) (azcertificates.GetDeletedCertificateResponse, error) {
				c := deletedCertificate(name)
				response := azcertificates.GetDeletedCertificateResponse{DeletedCertificate: azcertificates.DeletedCertificate{
					Attributes:         c.Attributes,
					ID:                 c.ID,
					RecoveryID:         c.RecoveryID,
					Tags:               c.Tags,
					X509Thumbprint:     c.X509Thumbprint,
					DeletedDate:        c.DeletedDate,
					ScheduledPurgeDate: c.ScheduledPurgeDate,
				}}
				if test.mutate != nil {
					test.mutate(&response)
				}
				return response, nil
			}
			if err := s.run(t.Context(), options(false)); err != nil {
				t.Fatal(err)
			}
			if (len(f.purges) == 1) != test.purged {
				t.Fatalf("purges = %v, want purge %t", f.purges, test.purged)
			}
		})
	}
}

func TestPurgeOnlySkipsActiveInventoryAndOwnerInventory(t *testing.T) {
	s, f, a, b := newTestSweeper("maestro-server-j1234567")
	f.deletedPages = [][]*azcertificates.DeletedCertificateProperties{
		{deletedCertificate("maestro-server-j2345678")},
		{deletedCertificate("maestro-server-j3456789")},
	}
	f.deletedListError = 2
	opts := options(false)
	opts.DeleteActive = false
	opts.MaxPurges = 1
	if err := s.run(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if f.activeLists != 0 || f.deletedLists != 1 {
		t.Fatalf("unexpected inventories: active=%d deleted=%d", f.activeLists, f.deletedLists)
	}
	if f.deletedPagesRead != 1 {
		t.Fatalf("must stop Key Vault paging at purge limit; read %d pages", f.deletedPagesRead)
	}
	if a.lists != 0 || b.lists != 0 {
		t.Fatalf("tombstones are not subject to the owner guard, but owners were inventoried: %d, %d", a.lists, b.lists)
	}
	if len(f.deletes) != 0 || !reflect.DeepEqual(f.purges, []string{"maestro-server-j2345678"}) {
		t.Fatalf("unexpected changes: deletes=%v purges=%v", f.deletes, f.purges)
	}
}

func TestImmediatePurgeWaitsForTombstone(t *testing.T) {
	s, f, _, _ := newTestSweeper("maestro-server-j1234567")
	s.pollInterval = time.Millisecond
	gets := 0
	f.getDeleted = func(name string) (azcertificates.GetDeletedCertificateResponse, error) {
		gets++
		if gets < 3 {
			return azcertificates.GetDeletedCertificateResponse{}, &azcore.ResponseError{StatusCode: 404}
		}
		c := deletedCertificate(name)
		return azcertificates.GetDeletedCertificateResponse{DeletedCertificate: azcertificates.DeletedCertificate{
			Attributes:         c.Attributes,
			ID:                 c.ID,
			RecoveryID:         c.RecoveryID,
			Tags:               c.Tags,
			X509Thumbprint:     c.X509Thumbprint,
			DeletedDate:        c.DeletedDate,
			ScheduledPurgeDate: c.ScheduledPurgeDate,
		}}, nil
	}

	if err := s.run(t.Context(), options(false)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.deletes, []string{"maestro-server-j1234567"}) {
		t.Fatalf("deletes = %v", f.deletes)
	}
	if len(f.deletedGets) != 4 {
		t.Fatalf("expected three tombstone polls and one purge revalidation, got %v", f.deletedGets)
	}
	if !reflect.DeepEqual(f.purges, []string{"maestro-server-j1234567"}) {
		t.Fatalf("purges = %v", f.purges)
	}
}

func TestPurgeBudgetsAreIndependent(t *testing.T) {
	s, f, _, _ := newTestSweeper(
		"maestro-server-j1234567",
		"maestro-server-j2345678",
		"maestro-server-j3456789",
	)
	f.deletedPages = [][]*azcertificates.DeletedCertificateProperties{{
		deletedCertificate("maestro-server-j4567890"),
		deletedCertificate("maestro-server-j5678901"),
	}}
	opts := options(false)
	opts.MaxDeletions = 2
	opts.MaxPurges = 1

	if err := s.run(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	// --max-deletions bounds active deletes (each purged immediately);
	// --max-purges bounds only the previously deleted tombstones.
	if len(f.deletes) != 2 {
		t.Fatalf("deletes = %v, want 2", f.deletes)
	}
	want := []string{"maestro-server-j1234567", "maestro-server-j2345678", "maestro-server-j4567890"}
	if !reflect.DeepEqual(f.purges, want) {
		t.Fatalf("purges = %v, want %v", f.purges, want)
	}
}

func TestTombstonePurgeIgnoresLiveOwner(t *testing.T) {
	s, f, a, _ := newTestSweeper()
	a.pages = [][]string{{"hcp-j1234567-rg"}}
	f.deletedPages = [][]*azcertificates.DeletedCertificateProperties{{deletedCertificate("maestro-server-j1234567")}}
	opts := options(false)
	opts.DeleteActive = false

	if err := s.run(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.purges, []string{"maestro-server-j1234567"}) {
		t.Fatalf("purges = %v; a lingering tombstone must be purged even if its owner token is live", f.purges)
	}
}

func TestWorkerLimit(t *testing.T) {
	s, f, _, _ := newTestSweeper(
		"maestro-server-j1234567",
		"maestro-server-j2345678",
		"maestro-server-j3456789",
		"maestro-server-j4567890",
	)
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	f.delete = func(string) error {
		started <- struct{}{}
		<-release
		return nil
	}
	opts := options(false)
	opts.PurgeDeleted = false
	opts.Workers = 2
	done := make(chan error, 1)
	go func() {
		done <- s.run(t.Context(), opts)
	}()

	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("workers did not start")
		}
	}
	select {
	case <-started:
		t.Fatal("more than two delete workers ran concurrently")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(f.deletes) != 4 {
		t.Fatalf("deletes = %v", f.deletes)
	}
}

func TestPurgeFailuresAndBoundedAttempts(t *testing.T) {
	for _, operation := range []string{"all failures", "partial failure", "purge 404", "GET 404", "success"} {
		t.Run(operation, func(t *testing.T) {
			s, f, _, _ := newTestSweeper()
			f.deletedPages = [][]*azcertificates.DeletedCertificateProperties{{
				deletedCertificate("maestro-server-j1234567"),
				deletedCertificate("maestro-server-j2345678"),
				deletedCertificate("maestro-server-j3456789"),
			}}
			opts := options(false)
			opts.MaxPurges = 2
			if strings.Contains(operation, "failure") {
				f.purge = func(name string) error {
					if operation == "partial failure" && name == "maestro-server-j2345678" {
						return nil
					}
					return errors.New("purge failed")
				}
			}
			if operation == "purge 404" {
				f.purge = func(string) error { return &azcore.ResponseError{StatusCode: 404} }
			}
			if operation == "GET 404" {
				f.getDeleted = func(string) (azcertificates.GetDeletedCertificateResponse, error) {
					return azcertificates.GetDeletedCertificateResponse{}, &azcore.ResponseError{StatusCode: 404}
				}
			}
			err := s.run(t.Context(), opts)
			if (err != nil) != (operation == "all failures") {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(f.deletedGets) != 2 {
				t.Fatalf("must process exactly selected purge limit: %v", f.deletedGets)
			}
			wantPurges := 2
			if operation == "GET 404" {
				wantPurges = 0
			}
			if len(f.purges) != wantPurges {
				t.Fatalf("purges = %v, want %d", f.purges, wantPurges)
			}
		})
	}
}

func TestFailuresAndBoundedAttempts(t *testing.T) {
	for _, operation := range []string{"all delete failures", "partial delete failure", "all GET failures", "partial GET failure", "delete 404", "GET 404", "success"} {
		t.Run(operation, func(t *testing.T) {
			s, f, _, _ := newTestSweeper("maestro-server-j1234567", "maestro-server-j2345678", "maestro-server-j3456789")
			opts := options(false)
			opts.MaxDeletions = 2
			var logs bytes.Buffer
			ctx := logr.NewContext(t.Context(), logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil)))
			if strings.Contains(operation, "delete failure") {
				f.delete = func(name string) error {
					if operation == "partial delete failure" && name == "maestro-server-j2345678" {
						return nil
					}
					return errors.New("delete failed")
				}
			}
			if operation == "delete 404" {
				f.delete = func(string) error {
					return &azcore.ResponseError{StatusCode: 404}
				}
			}
			if strings.Contains(operation, "GET failure") {
				f.get = func(name string) (azcertificates.GetCertificateResponse, error) {
					if operation == "partial GET failure" && name == "maestro-server-j2345678" {
						return latestCertificate(name), nil
					}
					return azcertificates.GetCertificateResponse{}, errors.New("GET failed")
				}
			}
			if operation == "GET 404" {
				f.get = func(string) (azcertificates.GetCertificateResponse, error) {
					return azcertificates.GetCertificateResponse{}, &azcore.ResponseError{StatusCode: 404}
				}
			}
			err := s.run(ctx, opts)
			wantError := strings.HasPrefix(operation, "all ")
			if (err != nil) != wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(f.gets) != 2 {
				t.Fatalf("must process exactly selected limit, even on failures: %v", f.gets)
			}
			wantDeletes := 2
			if strings.Contains(operation, "GET failure") {
				wantDeletes = 0
				if operation == "partial GET failure" {
					wantDeletes = 1
				}
			}
			if operation == "GET 404" {
				wantDeletes = 0
			}
			if len(f.deletes) != wantDeletes {
				t.Fatalf("deletes = %v, want %d", f.deletes, wantDeletes)
			}
			if err != nil && (!strings.Contains(err.Error(), "j1234567") || !strings.Contains(err.Error(), "j2345678")) {
				t.Fatalf("failure was not aggregated: %v", err)
			}
			if strings.Contains(operation, "partial") && (!strings.Contains(logs.String(), "Certificate cleanup attempts completed with errors") || !strings.Contains(logs.String(), "j1234567")) {
				t.Fatalf("partial failure was not reported at the end: %s", logs.String())
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

func TestNewSession(t *testing.T) {
	valid := func() Config {
		return Config{
			Client:       &fakeCertificates{},
			VaultURL:     testVaultURL,
			Policy:       repositoryPolicyForTests().Discovery,
			Owners:       func(context.Context) (sets.Set[string], time.Time, error) { return nil, time.Time{}, nil },
			DeleteActive: true,
			MaxDeletions: 1,
		}
	}
	if _, err := NewSession(valid()); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"no actions":                       func(c *Config) { c.DeleteActive = false },
		"zero deletion budget":             func(c *Config) { c.MaxDeletions = 0 },
		"zero purge budget":                func(c *Config) { c.PurgeDeleted = true },
		"owner guard rule needs inventory": func(c *Config) { c.Owners = nil },
		"unguarded delete rule": func(c *Config) {
			c.Policy = cleanuppolicy.CertificateDiscoveryPolicy{Rules: []cleanuppolicy.CertificateDiscoveryRule{{
				Name: "r", Action: cleanuppolicy.CertificateDiscoveryActionDelete, OlderThan: 24 * time.Hour,
				Match: cleanuppolicy.CertificateDiscoveryMatch{NameRegex: regexp.MustCompile(`^x$`)},
			}}}
		},
		"foreign vault URL": func(c *Config) { c.VaultURL = "not a url" },
		"no protected tags": func(c *Config) { c.Policy.ProtectedTags = nil },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := valid()
			mutate(&cfg)
			if _, err := NewSession(cfg); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
}

func TestTombstoneLeftByFailedImmediatePurgeIsPurgedInSameRun(t *testing.T) {
	s, f, _, _ := newTestSweeper("maestro-server-j1234567")
	// The tombstone listing is read after active certificates were deleted, so
	// it contains the tombstone whose immediate purge failed.
	f.deletedPages = [][]*azcertificates.DeletedCertificateProperties{{deletedCertificate("maestro-server-j1234567")}}
	purges := 0
	f.purge = func(string) error {
		purges++
		if purges == 1 {
			return errors.New("purge failed")
		}
		return nil
	}
	s.pollInterval = time.Millisecond

	if err := s.run(t.Context(), options(false)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.deletes, []string{"maestro-server-j1234567"}) {
		t.Fatalf("deletes = %v", f.deletes)
	}
	if !reflect.DeepEqual(f.purges, []string{"maestro-server-j1234567", "maestro-server-j1234567"}) {
		t.Fatalf("expected failed immediate purge to be retried by the tombstone step, purges = %v", f.purges)
	}
}
