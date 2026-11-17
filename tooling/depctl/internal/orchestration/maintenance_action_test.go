package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
)

const maintenanceHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const maintenancePrevious = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const maintenanceTime = "2026-11-05T15:00:00Z"

type actionForge struct {
	*forge.Fake
	snapshots []forge.Snapshot
	reads     int
	failRead  int
	writes    int
	body      string
	writeErr  error
	receipt   int
}

func (f *actionForge) Snapshot(_ context.Context, _ int, _ string) (forge.Snapshot, error) {
	f.reads++
	if f.failRead == f.reads {
		return forge.Snapshot{}, errors.New("API unavailable")
	}
	return f.snapshots[min(f.reads-1, len(f.snapshots)-1)], nil
}
func (f *actionForge) PostMaintenanceComment(_ context.Context, _ int, body string) (int, error) {
	f.writes++
	f.body = body
	return f.receipt, f.writeErr
}

func heldMaintenanceSnapshot() forge.Snapshot {
	pull, snapshot := inventorySnapshot(1, maintenanceHead)
	pull.Title, pull.HeadRef = policy.UpgradeTitle, "upgrade-agentic-workflows-daily"
	pull.Labels = []string{policy.HoldLabel}
	snapshot.Pull = pull
	snapshot.Commits = []string{maintenancePrevious, maintenanceHead}
	snapshot.Comments = []forge.Comment{{ID: 1, Author: policy.MergeBot, Body: "/hold\n\nRevision " + maintenancePrevious + " was retested 3 times: holding\n", CreatedAt: maintenanceTime, UpdatedAt: maintenanceTime}}
	snapshot.HoldEvents = []forge.HoldEvent{{ID: 1, Event: "labeled", Actor: policy.ProwBot, CreatedAt: maintenanceTime}}
	return snapshot
}

func TestMaintenanceFreshAuthorizationRaces(t *testing.T) {
	cases := []struct {
		name       string
		change     func(*forge.Snapshot)
		wantErr    bool
		wantWrites int
	}{
		{"unchanged", func(*forge.Snapshot) {}, false, 1},
		{"unrelated skipped check", func(s *forge.Snapshot) {
			s.Checks = []forge.Check{{Name: "optional", HeadSHA: maintenanceHead, Status: "completed", Conclusion: "skipped"}}
		}, false, 1},
		{"required E2E skipped", func(s *forge.Snapshot) {
			s.Statuses = s.Statuses[:1]
			s.Checks = []forge.Check{{Name: "ci/prow/e2e-parallel", HeadSHA: maintenanceHead, Status: "completed", Conclusion: "skipped"}}
		}, true, 0},
		{"required pipeline skipped", func(s *forge.Snapshot) {
			s.Statuses = s.Statuses[1:]
			s.Checks = []forge.Check{{Name: "ci/pipeline-gate", HeadSHA: maintenanceHead, Status: "completed", Conclusion: "skipped"}}
		}, true, 0},
		{"head moved", func(s *forge.Snapshot) { s.Pull.HeadSHA = maintenancePrevious }, true, 0},
		{"human hold", func(s *forge.Snapshot) {
			s.Comments = append(s.Comments, forge.Comment{ID: 2, Author: "reviewer", Body: "/hold", CreatedAt: maintenanceTime, UpdatedAt: maintenanceTime})
		}, true, 0},
		{"edited hold", func(s *forge.Snapshot) { s.Comments[0].UpdatedAt = "2026-11-06T15:00:00Z" }, true, 0},
		{"CI pending", func(s *forge.Snapshot) { s.Statuses[0].State = "pending" }, true, 0},
		{"CI failure", func(s *forge.Snapshot) { s.Statuses[0].State = "failure" }, true, 0},
		{"wrong check head", func(s *forge.Snapshot) {
			s.Checks = []forge.Check{{Name: "job", HeadSHA: maintenancePrevious, Status: "completed", Conclusion: "success"}}
		}, true, 0},
		{"human label", func(s *forge.Snapshot) { s.HoldEvents[0].Actor = "reviewer" }, true, 0},
		{"scope changed", func(s *forge.Snapshot) {
			s.Pull.Title = "fix(deps): update"
			s.Pull.Labels = append(s.Pull.Labels, policy.DependencyLabel)
		}, true, 0},
		{"closed", func(s *forge.Snapshot) { s.Pull.State = "closed" }, true, 0},
		{"merged", func(s *forge.Snapshot) { s.Pull.Merged = true }, true, 0},
		{"draft", func(s *forge.Snapshot) { s.Pull.Draft = true }, true, 0},
		{"label removed", func(s *forge.Snapshot) { s.Pull.Labels = []string{} }, false, 0},
		{"cancel arrived", func(s *forge.Snapshot) {
			s.Comments = append(s.Comments, forge.Comment{ID: 2, Author: policy.Robot, Body: "/hold cancel", CreatedAt: maintenanceTime, UpdatedAt: maintenanceTime})
		}, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first, second := heldMaintenanceSnapshot(), heldMaintenanceSnapshot()
			tc.change(&second)
			remote := &actionForge{Fake: forge.NewFake(), snapshots: []forge.Snapshot{first, second}, receipt: 9}
			result, id, err := New(Deps{Forge: remote}).ExecuteMaintenance(context.Background(), MaintenanceAction{Mode: "release-hold", Repository: "Azure/ARO-HCP", Number: 1, Head: maintenanceHead, Kind: "upgrade"}, remote)
			if (err != nil) != tc.wantErr || remote.writes != tc.wantWrites {
				t.Fatalf("error=%v writes=%d", err, remote.writes)
			}
			if tc.wantWrites == 1 && (remote.body != "/hold cancel" || result.ReleaseStatus != "cancel-requested" || id != 9 || remote.reads != 2) {
				t.Fatalf("unsafe transition: %+v id=%d remote=%+v", result, id, remote)
			}
		})
	}
}

func TestMaintenanceReadAndMutationFailuresAreNeverRetried(t *testing.T) {
	for _, fail := range []int{1, 2, 3, 4} {
		t.Run(string(rune('0'+fail)), func(t *testing.T) {
			remote := &actionForge{Fake: forge.NewFake(), snapshots: []forge.Snapshot{heldMaintenanceSnapshot()}, receipt: 9}
			if fail <= 2 {
				remote.failRead = fail
			} else if fail == 3 {
				remote.writeErr = errors.New("lost response")
			} else {
				remote.receipt = 0
			}
			_, _, err := New(Deps{Forge: remote}).ExecuteMaintenance(context.Background(), MaintenanceAction{Mode: "release-hold", Repository: "Azure/ARO-HCP", Number: 1, Head: maintenanceHead}, remote)
			if err == nil || remote.writes > 1 || (fail <= 2 && remote.writes != 0) {
				t.Fatalf("err=%v writes=%d", err, remote.writes)
			}
		})
	}
}

func TestMaintenanceRetryBudgetAndFixedCommands(t *testing.T) {
	failure := "https://prow.ci.openshift.org/view/gs/test/failure"
	evidence := "Transient infrastructure failure matched comparison " + failure + " and https://github.com/Azure/ARO-HCP/actions/runs/42"
	for _, mode := range []string{"recover-retry", "verify-upgrade", "assess"} {
		s := heldMaintenanceSnapshot()
		s.Statuses[0].State = "failure"
		s.Statuses[0].TargetURL = failure
		remote := &actionForge{Fake: forge.NewFake(), snapshots: []forge.Snapshot{s}, receipt: 9}
		_, _, err := New(Deps{Forge: remote}).ExecuteMaintenance(context.Background(), MaintenanceAction{Mode: mode, Repository: "Azure/ARO-HCP", Number: 1, Head: maintenanceHead, Evidence: evidence}, remote)
		if err != nil {
			t.Fatal(err)
		}
		if mode != "recover-retry" {
			if remote.writes != 0 {
				t.Fatal("read-only action wrote")
			}
			continue
		}
		expected := "<!-- " + policy.RecoveryMarker + maintenanceHead + " -->\n" + evidence + "\n\n/hold cancel\n/retest-required"
		if remote.body != expected {
			t.Fatalf("body=%q", remote.body)
		}
	}
	for _, mutation := range []string{"budget", "pending", "unknown", "no URL", "human", "missing comparison", "command", "marker", "short"} {
		t.Run(mutation, func(t *testing.T) {
			first, second := heldMaintenanceSnapshot(), heldMaintenanceSnapshot()
			for _, s := range []*forge.Snapshot{&first, &second} {
				s.Statuses[0].State = "failure"
				s.Statuses[0].TargetURL = failure
			}
			text := evidence
			switch mutation {
			case "budget":
				second.Comments = append(second.Comments, forge.Comment{ID: 2, Author: policy.Robot, Body: "<!-- " + policy.RecoveryMarker + maintenanceHead + " -->", CreatedAt: maintenanceTime})
			case "pending":
				second.Statuses[0].State = "pending"
			case "unknown":
				second.Statuses[0].State = "mystery"
			case "no URL":
				second.Statuses[0].TargetURL = ""
			case "human":
				second.Comments[0].Author = "human"
			case "missing comparison":
				text = "Transient failure identified at " + failure
			case "command":
				text += "\n /retest"
			case "marker":
				text += policy.RecoveryMarker
			case "short":
				text = "short"
			}
			remote := &actionForge{Fake: forge.NewFake(), snapshots: []forge.Snapshot{first, second}, receipt: 9}
			_, _, err := New(Deps{Forge: remote}).ExecuteMaintenance(context.Background(), MaintenanceAction{Mode: "recover-retry", Repository: "Azure/ARO-HCP", Number: 1, Head: maintenanceHead, Evidence: text}, remote)
			if err == nil || remote.writes != 0 {
				t.Fatalf("err=%v writes=%d", err, remote.writes)
			}
		})
	}
}

func TestMaintenanceInvalidTargetsNeverRead(t *testing.T) {
	for _, change := range []func(*MaintenanceAction){func(a *MaintenanceAction) { a.Repository = "../repo" }, func(a *MaintenanceAction) { a.Number = 0 }, func(a *MaintenanceAction) { a.Head = "short" }, func(a *MaintenanceAction) { a.Kind = "images" }, func(a *MaintenanceAction) { a.Mode = "shell" }} {
		remote := &actionForge{Fake: forge.NewFake()}
		a := MaintenanceAction{Mode: "assess", Repository: "Azure/ARO-HCP", Number: 1, Head: maintenanceHead}
		change(&a)
		if _, _, err := New(Deps{Forge: remote}).ExecuteMaintenance(context.Background(), a, remote); err == nil || remote.reads != 0 {
			t.Fatalf("action=%+v reads=%d err=%v", a, remote.reads, err)
		}
	}
}

func TestScopedInventoryFiltersBeforeSnapshot(t *testing.T) {
	fake := forge.NewFake()
	pull, snapshot := inventorySnapshot(1, maintenanceHead)
	fake.Seed(pull, snapshot)
	fake.FailOn("snapshot")
	result, err := New(Deps{Forge: fake}).MaintenanceInventory(context.Background(), "Azure/ARO-HCP", "upgrade")
	if err != nil || len(result) != 0 {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

func TestScopedInventoryRechecksSnapshotKind(t *testing.T) {
	fake := forge.NewFake()
	pull, snapshot := inventorySnapshot(1, maintenanceHead)
	fake.Seed(pull, snapshot)
	snapshot.Pull.Title = policy.UpgradeTitle
	snapshot.Pull.HeadRef = "upgrade-agentic-workflows-daily"
	remote := &actionForge{Fake: fake, snapshots: []forge.Snapshot{snapshot}}
	if _, err := New(Deps{Forge: remote}).MaintenanceInventory(context.Background(), "Azure/ARO-HCP", "non-compiler"); err == nil {
		t.Fatal("scope race accepted")
	}
}

func TestUnknownMarkerAuthorDoesNotConsumeBudget(t *testing.T) {
	s := heldMaintenanceSnapshot()
	s.Statuses[0].State = "failure"
	s.Statuses[0].TargetURL = "https://prow.ci.openshift.org/failure"
	s.Comments = append(s.Comments, forge.Comment{ID: 2, Author: "human", Body: "<!-- " + policy.RecoveryMarker + maintenanceHead + " -->", CreatedAt: maintenanceTime})
	d, err := policy.Assess("Azure/ARO-HCP", 1, maintenanceHead, s)
	if err != nil || !d.CanRecoverRetry {
		t.Fatalf("err=%v blockers=%v", err, d.RetryBlockers)
	}
	if strings.Contains(strings.Join(d.RetryBlockers, " "), "consumed") {
		t.Fatal("untrusted budget marker accepted")
	}
}
