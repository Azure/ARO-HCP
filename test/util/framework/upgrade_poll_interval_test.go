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

package framework

import (
	"testing"
	"time"
)

// The ARO-26775 control plane y-stream upgrade timeline, 2026-05-06, as recorded on the ticket.
//
//	00:39:38.159  the spec enters "verifying control plane reached desired version" and the
//	              45 minute Eventually starts
//	01:24:16.000  4.21.13 appears in clusterversion status.history
//	01:24:38.167  the Eventually expires, 2700.008s after it started, and the run goes red
//
// The upgrade finished inside its budget. The run failed because the poller was not looking: at a
// 2 minute interval the preceding poll had fired at 01:23:38, and the next one was due 60s past
// the deadline.
const (
	// aro26775ConditionAt is how long after the Eventually started that the target version
	// landed: 01:24:16.000 - 00:39:38.159.
	aro26775ConditionAt = 44*time.Minute + 37*time.Second + 841*time.Millisecond

	// intervalBeforeARO26775Fix is the poll interval the three control plane upgrade specs used
	// before this change.
	intervalBeforeARO26775Fix = 2 * time.Minute
)

// firstPollAtOrAfter returns the time of the first poll that observes a condition which becomes
// true at conditionAt, for a poller that starts with an immediate poll at t=0 and then ticks every
// interval.
//
// This is an idealised fixed schedule, and the specs do not quite follow it. Gomega's Eventually
// waits time.After(interval) *after* each attempt returns (gomega internal/async_assertion.go), so
// its real period is interval plus however long the attempt took -- here a whole VerifyHCPCluster
// fan-out -- and the drift accumulates over the run. wait.PollUntilContextTimeout, which
// verifiers.pollUntilReady uses, passes sliding=false and so measures the interval inclusive of the
// condition, which does not drift.
//
// The absolute poll times below are therefore optimistic for the Eventually-based specs. The
// conclusion drawn from them is not: worst-case detection latency is one effective period, so
// cutting the interval by 110s cuts worst-case latency by 110s whatever each attempt costs. The
// drift itself is an argument for moving these specs onto pollUntilReady, tracked separately.
func firstPollAtOrAfter(interval, conditionAt time.Duration) time.Duration {
	if conditionAt <= 0 {
		return 0
	}
	polls := (conditionAt + interval - 1) / interval // ceiling division
	return polls * interval
}

func TestFirstPollAtOrAfter(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		interval    time.Duration
		conditionAt time.Duration
		expected    time.Duration
	}{
		{name: "already true at t=0", interval: 10 * time.Second, conditionAt: 0, expected: 0},
		{name: "lands exactly on a tick", interval: 10 * time.Second, conditionAt: 30 * time.Second, expected: 30 * time.Second},
		{name: "lands just after a tick", interval: 10 * time.Second, conditionAt: 31 * time.Second, expected: 40 * time.Second},
		{name: "lands just before a tick", interval: 10 * time.Second, conditionAt: 39 * time.Second, expected: 40 * time.Second},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if actual := firstPollAtOrAfter(testCase.interval, testCase.conditionAt); actual != testCase.expected {
				t.Errorf("firstPollAtOrAfter(%s, %s) = %s, want %s",
					testCase.interval, testCase.conditionAt, actual, testCase.expected)
			}
		})
	}
}

// TestUpgradePollIntervalDetectsTheARO26775Upgrade replays the recorded timeline against the poll
// interval the control plane upgrade specs actually use, and fails if that interval would once
// again miss an upgrade that completed inside its budget.
//
// This is a guard on the constants, not on firstPollAtOrAfter: raising StandardPollInterval or
// lowering HCPClusterVersionUpgradeTimeout far enough to reintroduce the failure breaks this test
// and says why.
func TestUpgradePollIntervalDetectsTheARO26775Upgrade(t *testing.T) {
	budget := HCPClusterVersionUpgradeTimeout

	if aro26775ConditionAt >= budget {
		t.Fatalf("the recorded upgrade (%s) did not finish inside the budget (%s), so this timeline no longer describes a near-miss",
			aro26775ConditionAt, budget)
	}
	margin := budget - aro26775ConditionAt

	detectedBefore := firstPollAtOrAfter(intervalBeforeARO26775Fix, aro26775ConditionAt)
	if detectedBefore <= budget {
		t.Fatalf("the %s interval would have detected the upgrade at %s, inside the %s budget; "+
			"the recorded run went red, so this test no longer reproduces ARO-26775",
			intervalBeforeARO26775Fix, detectedBefore, budget)
	}

	detectedNow := firstPollAtOrAfter(StandardPollInterval, aro26775ConditionAt)
	if detectedNow > budget {
		t.Errorf("ARO-26775 would recur: the upgrade completes at %s with %s of budget to spare, "+
			"but a %s poll interval does not observe it until %s, past the %s budget",
			aro26775ConditionAt, margin, StandardPollInterval, detectedNow, budget)
	}

	t.Logf("ARO-26775 timeline: upgrade completed at %s, %s inside the %s budget", aro26775ConditionAt, margin, budget)
	t.Logf("  %-6s interval: observed at %s -> %s", intervalBeforeARO26775Fix, detectedBefore, verdict(detectedBefore, budget))
	t.Logf("  %-6s interval: observed at %s -> %s", StandardPollInterval, detectedNow, verdict(detectedNow, budget))
	t.Logf("  detection is %s earlier, which is what turns this run from red to green", detectedBefore-detectedNow)
}

// TestUpgradePollIntervalFitsTheObservedMargin states the general form of the same requirement.
//
// Worst case, a condition that becomes true immediately after a poll is not observed for a further
// interval. So for an upgrade finishing with margin m to spare to be seen at all, the interval must
// be no larger than m. ARO-26775 recorded m = 22.159s.
//
// This bounds the miss window; it does not close it. A condition landing in the last interval
// before the deadline is still missed, which is what a post-deadline final check would address --
// tracked separately, out of scope here.
func TestUpgradePollIntervalFitsTheObservedMargin(t *testing.T) {
	margin := HCPClusterVersionUpgradeTimeout - aro26775ConditionAt

	if StandardPollInterval > margin {
		t.Errorf("a poll interval of %s cannot reliably observe an upgrade that finishes with only %s to spare, "+
			"which is the margin ARO-26775 recorded; keep the interval at or below that margin",
			StandardPollInterval, margin)
	}

	worstCaseLatencyBefore := intervalBeforeARO26775Fix
	worstCaseLatencyNow := StandardPollInterval
	t.Logf("worst-case detection latency: %s before, %s now (%s saved per upgrade wait)",
		worstCaseLatencyBefore, worstCaseLatencyNow, worstCaseLatencyBefore-worstCaseLatencyNow)
}

func verdict(detectedAt, budget time.Duration) string {
	if detectedAt > budget {
		return "MISSED, run fails " + (detectedAt - budget).String() + " past the deadline"
	}
	return "detected, " + (budget - detectedAt).String() + " inside the deadline"
}
