# hcpctl Improvement Tasks

This document contains specific actionable tasks based on the improvement opportunities identified in `improvements.md`.

## 1. CLI Commands Improvements

### 1.1 Make Arguments Mandatory for breakglass Commands
**Priority: High**  
**Effort: Medium**

**Current State**: Both `breakglass mc` and `breakglass hcp` accept optional arguments and fall back to interactive selection.

**Tasks**:
- [x] **Update `breakglass mc` command**: Make `AKS_NAME` argument mandatory
  - [x] Change `cobra.MaximumNArgs(1)` to `cobra.ExactArgs(1)` in `cmd/breakglass/mc/cmd.go:49`
  - [x] Remove interactive selection logic from `runBreakglass()` in `cmd/breakglass/mc/cmd.go:100-110`
  - [x] Update command help text to reflect mandatory argument
  - [x] Update usage examples in command descriptions

- [x] **Update `breakglass hcp` command**: Make `CLUSTER_ID_OR_RESOURCE_ID` argument mandatory  
  - [x] Change `cobra.MaximumNArgs(1)` to `cobra.ExactArgs(1)` in `cmd/breakglass/hcp/cmd.go:52`
  - [x] Remove interactive selection logic from `runBreakglass()` 
  - [x] Update command help text to reflect mandatory argument
  - [x] Update usage examples in command descriptions

- [x] **Remove cluster selection code**: 
  - [x] Delete `cmd/breakglass/mc/select.go` (entire file no longer needed)
  - [x] Delete `cmd/breakglass/hcp/select.go` (entire file no longer needed)
  - [x] Remove `SelectCluster()` function calls
  - [x] Clean up imports related to interactive selection (survey package)
  - [x] Update HCP options validation to require cluster identifier
  - [x] Update existing tests to reflect mandatory argument requirement
  - [x] Create new tests for MC command argument validation

**Benefits**: Clearer CLI interface, more scriptable, consistent with listing being available via `list` subcommand

### 1.2 Add Region Filtering to MC List Command
**Priority: Medium**  
**Effort: Low**

**Current State**: `breakglass mc list` shows all clusters without filtering options.

**Tasks**:
- [x] **Add --region flag**: 
  - [x] Add `Region string` field to `RawMCOptions` struct in `cmd/breakglass/mc/options.go:35`
  - [x] Bind `--region` flag in `BindMCOptions()` in `cmd/breakglass/mc/options.go:74`
  - [ ] Add validation for region format (optional - validate against known Azure regions)

- [x] **Implement filtering logic**:
  - [x] Modify `runListClusters()` in `cmd/breakglass/mc/cmd.go:152` to filter by region
  - [x] Add region filtering to discovered clusters before `DisplayClustersTable()`
  - [x] Update help text to document the `--region` flag

- [x] **Add tests**:
  - [x] Add unit tests for region filtering logic
  - [x] Test edge cases (unknown regions, case sensitivity)

**Benefits**: Faster cluster discovery in specific regions, better user experience for large environments

## 2. Shell Code Improvements

### 2.1 Eliminate Shell Code Duplication
**Priority: High**  
**Effort: Medium**

**Current State**: Shell handling code exists in multiple places:
- `cmd/breakglass/mc/shell.go` - MC shell handling
- `pkg/breakglass/execute.go:494-613` - HCP shell handling (`runShellMode()`, `getDefaultShell()`)

**Tasks**:
- [ ] **Analyze differences**: 
  - [ ] Compare shell detection logic between MC and HCP implementations
  - [ ] Document differences in prompt handling, environment setup
  - [ ] Identify common functionality that can be extracted

- [ ] **Create unified shell package**:
  - [ ] Create `pkg/shell/` directory structure
  - [ ] Design common interface for shell operations
  - [ ] Account for different prompt requirements (MC vs HCP cluster info)

- [ ] **Extract common shell detection**:
  - [ ] Move `detectShell()` from `cmd/breakglass/mc/shell.go:92` to `pkg/shell/detect.go`
  - [ ] Move `getDefaultShell()` from `pkg/breakglass/execute.go:592` to same location
  - [ ] Unify the logic (they're nearly identical but slightly different)

### 2.2 Create pkg/shell Package
**Priority: High**  
**Effort: Medium**

**Tasks**:
- [ ] **Create package structure**:
  ```
  pkg/shell/
  ├── shell.go          # Main shell interface and types
  ├── detect.go         # Shell detection logic  
  ├── environment.go    # Environment variable setup
  ├── prompt.go         # Prompt customization
  └── spawn.go          # Shell spawning logic
  ```

- [ ] **Design shell interface**:
  ```go
  type Shell interface {
      Spawn(ctx context.Context, config *Config) error
  }
  
  type Config struct {
      KubeconfigPath string
      ClusterName    string
      ClusterID      string
      CustomEnv      []string
      PromptInfo     string
  }
  ```

- [ ] **Implement unified shell logic**:
  - [ ] Cross-platform shell detection (Windows PowerShell/cmd, Unix bash/sh)
  - [ ] Environment variable setup (KUBECONFIG, noise suppression)
  - [ ] Custom prompt generation (with cluster context)
  - [ ] Shell spawning with proper I/O handling

### 2.3 Review Shell Code for Simplicity
**Priority: Medium**  
**Effort: Low**

**Current State**: Shell code has grown complex with multiple OS paths, prompt handling, environment setup.

**Tasks**:
- [ ] **Simplify shell detection**:
  - [ ] Remove redundant shell detection logic
  - [ ] Use consistent preference order across platforms
  - [ ] Reduce OS-specific branches where possible

- [ ] **Simplify environment setup**:
  - [ ] Extract environment variable setup to dedicated function
  - [ ] Remove duplicate environment variable setting
  - [ ] Consolidate kubectl noise suppression logic

- [ ] **Improve error handling**:
  - [ ] Add consistent error handling for shell failures
  - [ ] Provide better error messages for missing shells
  - [ ] Handle edge cases (shell not found, permission issues)

- [ ] **Improve readability**:
  - [ ] Extract long inline functions to named functions
  - [ ] Add better code comments explaining platform differences
  - [ ] Use consistent naming conventions

## 3. Cleanup Code Improvements  

### 3.1 Investigate vs Defer Solution
**Priority: Medium**  
**Effort: Medium**

**Current State**: Complex cleanup tracking system in `pkg/breakglass/cleanup/` with custom `Tracker` type.

**Tasks**:
- [ ] **Analyze current cleanup system**:
  - [ ] Review `pkg/breakglass/cleanup/tracker.go` functionality
  - [ ] Identify all cleanup operations (CSR, CSR approval, port forwarding)
  - [ ] Document cleanup order requirements (LIFO vs other)
  - [ ] Measure complexity vs benefit of current approach

- [ ] **Design simpler defer-based solution**:
  - [ ] Prototype cleanup using Go's native `defer` statements
  - [ ] Handle cleanup ordering with nested functions or careful defer placement
  - [ ] Compare error handling capabilities (defer vs tracker)
  - [ ] Consider context cancellation scenarios

- [ ] **Evaluate trade-offs**:
  - [ ] **Current system pros**: Explicit cleanup tracking, error aggregation, logging per cleanup
  - [ ] **Current system cons**: Added complexity, extra abstraction, more code to maintain
  - [ ] **Defer pros**: Native Go idiom, simpler code, automatic cleanup
  - [ ] **Defer cons**: Less explicit tracking, harder error aggregation

- [ ] **Implementation decision**:
  - [ ] Choose between current tracker vs defer approach based on analysis
  - [ ] If defer: Refactor `pkg/breakglass/execute.go` to use defer statements
  - [ ] If tracker: Simplify tracker interface and reduce complexity
  - [ ] Document decision and reasoning

## 4. Package Organization

### 4.1 Review pkg/cluster Package Location
**Priority: Low**  
**Effort: Low**

**Current State**: `pkg/cluster/` contains both HCP and AKS discovery logic.

**Tasks**:
- [ ] **Analyze package responsibilities**:
  - [ ] Review contents of `pkg/cluster/`: 
    - `discovery.go` - HCP cluster discovery
    - `aks_discovery.go` - AKS cluster discovery  
    - `validation.go` - Resource ID validation
  - [ ] Evaluate if cluster discovery fits in this package vs alternatives

- [ ] **Consider alternative package structures**:
  - [ ] Option 1: Keep current structure (discovery is cluster-related)
  - [ ] Option 2: Move to `pkg/discovery/` (focus on discovery functionality)
  - [ ] Option 3: Split into `pkg/hcp/` and `pkg/aks/` (separate by cloud type)
  - [ ] Option 4: Move to `pkg/resources/` (focus on Azure resource operations)

- [ ] **Evaluate package cohesion**:
  - [ ] Check if cluster discovery belongs with other cluster operations
  - [ ] Consider future additions (cluster management, status, etc.)
  - [ ] Review Go package organization best practices

- [ ] **Make recommendation**:
  - [ ] Document analysis of current vs alternative structures
  - [ ] Recommend keeping current structure unless strong reasons to change
  - [ ] If changes needed, create migration plan with minimal disruption

## Priority Summary

**High Priority** (should be addressed first):
1. Shell code deduplication and pkg/shell package creation
2. Make CLI arguments mandatory  

**Medium Priority** (should be addressed next):
1. Region filtering for MC list
2. Shell code simplicity review
3. Cleanup system evaluation

**Low Priority** (nice to have):
1. Package organization review

## Dependencies

- Shell improvements (2.1, 2.2) should be done together as they're closely related
- CLI argument changes (1.1) should be done before region filtering (1.2) to avoid conflicts
- Cleanup investigation (3.1) can be done independently
- Package review (4.1) can be done independently and last