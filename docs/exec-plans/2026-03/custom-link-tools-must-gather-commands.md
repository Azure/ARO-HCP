# Plan: Add "Commands (must-gather, etc.)" HTML output to custom-link-tools

## Context

The custom-link-tools command (in `test/cmd/aro-hcp-tests/custom-link-tools/`) generates HTML files with Kusto links for OCP's spyglass display during CI test runs. It already computes timing windows (start/end times), cluster names (svc/mgmt), and Kusto connection details. We want to reuse this data to also generate a third HTML file containing ready-to-paste `hcpctl must-gather` CLI commands, so engineers can quickly gather logs from the relevant clusters and time window.

## Files to Modify

1. **`test/cmd/aro-hcp-tests/custom-link-tools/options.go`** - refactor time window computation out of `getServiceLogLinks`, add `--subscription-id` CLI flag, add command generation logic, render new template in `Run`
2. **`test/cmd/aro-hcp-tests/custom-link-tools/artifacts/custom-link-tools-commands.html.tmpl`** - new HTML template (create)
3. **`test/cmd/aro-hcp-tests/custom-link-tools/cmd_test.go`** - add fixture comparison for the new HTML file
4. **`test/cmd/testdata/zz_fixture_*.html`** - new fixture files (auto-generated via `UPDATE=true`)

## Implementation

### Step 0: Write exec plan
Write the full plan to `docs/exec-plans/2026-03/custom-link-tools-must-gather-commands.md`.

### Step 1: Extract time window computation

Extract the start/end time logic from `getServiceLogLinks` into a new function in `options.go`:

```go
type TimeWindow struct {
    Start time.Time
    End   time.Time
}

func computeTimeWindow(logger logr.Logger, steps []pipeline.NodeInfo, testTimingInfo map[string]TimingInfo, startTimeFallback *time.Time) (TimeWindow, error)
```

This function contains the existing logic from `getServiceLogLinks` (lines 533-600) that determines `earliestStartTime` and `endTime` from steps, test timing, CLI fallback, and clock fallback.

Then `getServiceLogLinks` takes `TimeWindow` as a parameter instead of computing it internally.

### Step 2: Add `--subscription-id` CLI flag, `CommandInfo` type, and generation function

Add optional `--subscription-id` flag to `RawOptions` / `BindOptions` / `completedOptions` / `Complete`. When provided via CLI flag, the value is included in the generated command; when omitted, the flag is emitted with an empty value for the user to fill in.

In `options.go`:

```go
type CommandInfo struct {
    Label   string
    Command string
}
```

Add a function to generate the must-gather commands:

```go
func getMustGatherCommands(tw TimeWindow, svcClusterName, mgmtClusterName, subscriptionID string, kusto KustoInfo) []CommandInfo
```

This produces three commands:
1. **query-infra (SVC cluster)**: `hcpctl must-gather query-infra --kusto <name> --region <region> --infra-cluster <svcCluster> --timestamp-min "<start>" --timestamp-max "<end>"`
2. **query-infra (MGMT cluster)**: same but with mgmt cluster name
3. **query**: `hcpctl must-gather query --kusto <name> --region <region> --timestamp-min "<start>" --timestamp-max "<end>" --subscription-id <subscriptionID>`

Time format: use `time.DateTime` (`2006-01-02 15:04:05`) since that's the format `hcpctl must-gather` flags accept. Quote the values in the command string since they contain spaces.

When the flag is not provided, the `query` command includes `--subscription-id` with an empty value for the user to fill in.

### Step 3: Create HTML template

Create `artifacts/custom-link-tools-commands.html.tmpl` with title "Commands (must-gather, etc.)". Style commands in `<pre>` blocks for easy copy-paste. Follow the same spyglass CSS conventions as the existing templates.

### Step 4: Update `Run` to generate the new file

In `Run()`:
1. Call `computeTimeWindow(...)` to get the `TimeWindow`
2. Pass `TimeWindow` to `getServiceLogLinks` (updated signature)
3. Call `getMustGatherCommands(tw, ..., o.SubscriptionID, ...)` to get commands
4. Render the new template to `custom-link-tools-commands.html`

### Step 5: Update tests

In `cmd_test.go`:
- In `TestGeneratedHTML`: set `SubscriptionID` to a test value and add `testutil.CompareFileWithFixture` for the new `custom-link-tools-commands.html` output
- In `TestGeneratedHTMLWithoutStepsUsesTimingFallback`: leave it empty and add fixture comparison (tests the "user fills it in" case)
- Update `assertAllServiceLinkQueriesContainTimeWindow` or its callers if the `getServiceLogLinks` signature changes
- Run tests with `UPDATE=true` to generate fixture files

### Step 6: Verify

```bash
cd test && UPDATE=true go test ./cmd/aro-hcp-tests/custom-link-tools/ -run TestGenerated -count=1
cd test && go test ./cmd/aro-hcp-tests/custom-link-tools/ -count=1
```

Inspect generated fixture files to confirm the commands look correct - in particular that the subscription ID appears when provided and is an empty placeholder when not.

### Step 7: Add execution summary to exec plan

## Execution Summary

All steps were executed as planned with no deviations.

### Changes made

1. **`options.go`**: Added `SubscriptionID` field to `RawOptions` and `completedOptions`, registered `--subscription-id` flag in `BindOptions`, passed it through in `Complete`. Extracted `computeTimeWindow()` from `getServiceLogLinks()`, added `TimeWindow` struct, `CommandInfo` struct, and `getMustGatherCommands()` function (accepting `subscriptionID` parameter). Updated `getServiceLogLinks()` signature to accept `TimeWindow` instead of computing it internally (also changed return type from `([]LinkDetails, error)` to `[]LinkDetails` since it no longer parses times). Updated `Run()` to call `computeTimeWindow()`, pass result to both `getServiceLogLinks()` and `getMustGatherCommands()`, and render the new commands HTML template.

2. **`artifacts/custom-link-tools-commands.html.tmpl`**: New template with dark-themed styling matching the test-table template, rendering commands in `<pre>` blocks with `<h3>` labels.

3. **`cmd_test.go`**: Added `CompareFileWithFixture` calls for the new commands HTML in both `TestGeneratedHTML` (suffix `custom-link-tools-commands`, with `SubscriptionID: "00000000-0000-0000-0000-000000000000"`) and `TestGeneratedHTMLWithoutStepsUsesTimingFallback` (suffix `custom-link-tools-commands-no-steps`, no subscription ID). Updated `TestGetServiceLogLinksUsesClockFallbackWhenNoStepsAndNoTiming` and `TestGetServiceLogLinksUsesCLIStartFallbackWhenStepsAndTimingUnavailable` to call `computeTimeWindow()` + updated `getServiceLogLinks()` signature.

4. **Fixture files generated**:
   - `test/cmd/testdata/zz_fixture_TestGeneratedHTMLcustom_link_tools_commands.html`
   - `test/cmd/testdata/zz_fixture_TestGeneratedHTMLWithoutStepsUsesTimingFallbackcustom_link_tools_commands_no_steps.html`

All 5 tests pass.

## Addendum: Post-commit tweaks

Follow-up tweak applied after the initial commit:

### Commented out `must-gather query` command
The `hcpctl must-gather query` command presentation isn't useful like this. Commented out the `queryCmd` variable and the third `CommandInfo` entry in `getMustGatherCommands()` in `options.go`. The `subscriptionID` parameter, CLI flags, and struct fields are kept in place for the follow-up.
