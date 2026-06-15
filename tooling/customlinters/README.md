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
linters:
  enable:
  - noprint
  settings:
    custom:
      noprint:
        type: "module"
        description: Detects print statements to standard output in test files
```

## Testing

The custom linters use Go unit tests to validate analyzer behavior.

### Test Structure

```
tooling/customlinters/
├── noprintlinter_test.go    # Go unit tests
└── testdata/
    └── src/
        └── noprint/
            └── test/
                ├── positive_test.go   # Code that should trigger the linter
                └── negative_test.go   # Valid code that should not trigger the linter
```

### Running Tests

```bash
make test         # Run all unit tests
```

### Writing Unit Tests

Tests use [`golang.org/x/tools/go/analysis/analysistest`](https://pkg.go.dev/golang.org/x/tools/go/analysis/analysistest), which runs the analyzer against a real Go package under `testdata/src/`. Any diagnostic the analyzer emits must be declared as expected via a `` // want `pattern` `` annotation on the same source line, otherwise `analysistest.Run` fails the test with "unexpected diagnostic".

Test pattern in `noprintlinter_test.go`:

1. Retrieve the plugin constructor via `register.GetPlugin("noprint")`
2. Instantiate the plugin by calling the constructor with `nil` settings: `newPlugin(nil)`
3. Build the analyzers with `plugin.BuildAnalyzers()` and assert exactly one is returned
4. Run `analysistest.Run(t, analysistest.TestData(), analyzers[0], "noprint")` where `"noprint"` matches the package directory under `testdata/src/`

#### Test Data

Test files live under `testdata/src/noprint/`:
- **`positive_test.go`** — Code with violations the linter should catch. Each offending line must carry a `` // want `pattern` `` annotation so `analysistest` knows the diagnostic is expected.
- **`negative_test.go`** — Valid code (including suppressed lines) that the linter should not flag.

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

**Suppressing Violations:**

When a print statement is genuinely necessary in a test file, you can suppress the linter on a per-call basis using either of two directives:

| Directive | Form |
|---|---|
| `// nolint:noprint` | golangci-lint standard |
| `// noprint:ignore` | project-specific alternative |

Two placement styles are supported:

1. **Inline** — directive on the same line as the call:
   ```go
   fmt.Println("intentional output") // nolint:noprint
   ```

2. **Previous-line** — directive on its own line immediately above the call (no code on the same line as the directive):
   ```go
   // noprint:ignore
   fmt.Printf("also suppressed: %s\n", v)
   ```

**Implementation:** `noprintlinter.go`
