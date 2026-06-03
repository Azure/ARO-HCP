# Custom Linters

This directory contains custom golangci-lint plugins for the ARO-HCP project.

## Prerequisites

You need `golangci-lint` installed. This project uses [bingo](https://github.com/bwplotka/bingo) to manage tool versions, ensuring consistency with CI/CD automation (version defined in `.bingo/golangci-lint.mod`).

## Usage

### Automatic (Recommended)

The custom linters are automatically run as part of `make lint` from the repository root:

```bash
make lint
```

This runs:
1. Standard golangci-lint with built-in linters on all modules
2. Custom golangci-lint with custom linters on all modules

### Build

From the `tooling/customlinters` directory:

```bash
make build   # Build custom linter binary
```

### Manual Usage against test directory

**From repository root:**
```bash
./tooling/customlinters/bin/custom-golangci-lint run --build-tags=E2Etests --config=tooling/customlinters/.golangci-custom.yml ./test/...
```

**From `tooling/customlinters` directory:**
```bash
./bin/custom-golangci-lint run --build-tags=E2Etests --config=.golangci-custom.yml ../../test/...
```

**Note:** The `--config` flag is required to enable the custom linters.

## Configuration

### Build Configuration (`.custom-gcl.yml`)

Defines which custom linter plugins to compile into the binary:

```yaml
version: v2.1.6
name: custom-golangci-lint
destination: ./bin
plugins:
  - module: 'github.com/Azure/ARO-HCP/tooling/customlinters'
    import: 'github.com/Azure/ARO-HCP/tooling/customlinters'
    path: .
```

### Runtime Configuration (`.golangci-custom.yml`)

Defines which linters to run and their settings:

```yaml
version: "2"
run:
  timeout: 10m
  skip-dirs:
    - tooling/customlinters/testdata  # Don't lint test data
issues:
  max-issues-per-linter: 0
  max-same-issues: 0
linters:
  enable:
    - noprint  # Enable the noprint linter
```

## Testing

The custom linters use Go unit tests to validate analyzer behavior.

### Test Structure

```
tooling/customlinters/
├── noprintlinter_test.go    # Go unit tests
└── testdata/
    ├── positive_test.go     # Code that should trigger linter
    └── negative_test.go     # Valid code that should not trigger linter
```

### Running Tests

```bash
make test         # Run all unit tests
```

### Writing Unit Tests

Each linter should have corresponding unit tests following the pattern in `noprintlinter_test.go`:

1. **Positive test cases** - Verify the linter detects violations
2. **Negative test cases** - Verify the linter allows valid code

**Example test structure:**
```go
func TestLinter_PositiveCases(t *testing.T) {
    // 1. Build the analyzer
    linter := &YourLinter{}
    analyzers, _ := linter.BuildAnalyzers()

    // 2. Parse test file with path matching linter's filter
    fset := token.NewFileSet()
    content, _ := os.ReadFile("testdata/positive_test.go")
    file, _ := parser.ParseFile(fset, "/fake/path/test/positive_test.go", content, parser.ParseComments)

    // 3. Run analyzer and count diagnostics
    diagnosticCount := 0
    pass := &analysis.Pass{
        Fset: fset,
        Files: []*ast.File{file},
        Report: func(d analysis.Diagnostic) {
            diagnosticCount++
        },
    }
    analyzer.Run(pass)

    // 4. Verify expected count
    if diagnosticCount != expectedIssues {
        t.Errorf("Expected %d issues, got %d", expectedIssues, diagnosticCount)
    }
}
```

### Test Data

Place test files in `testdata/` directory:
- **positive_test.go** - Code with violations the linter should catch
- **negative_test.go** - Valid code the linter should allow

The `testdata/` directory is excluded from the main lint run to avoid flagging intentional test violations.

## Available Linters

### No Print Linter

**Purpose:** Detects and prevents the use of print statements to standard output in test files.

E2E tests should use proper testing output mechanisms (like Ginkgo's `GinkgoLogr` or `GinkgoWriter`) instead of direct writes to standard output, which can interfere with test runners and cause output parsing failures in OTE.

**Detected Functions:**

The linter catches the following functions in files under the `test/` directory:

- ❌ `fmt.Print`, `fmt.Printf`, `fmt.Println`
- ❌ `log.Print`, `log.Printf`, `log.Println`
- ❌ Builtin `print` and `println`

**Allowed Alternatives:**

- ✅ `fmt.Sprintf`, `fmt.Errorf` (don't print to stdout)
- ✅ `t.Log()`, `t.Logf()` (standard Go test logging)
- ✅ `GinkgoWriter`, `GinkgoLogr` (Ginkgo test output)

**Implementation:** `noprintlinter.go`
