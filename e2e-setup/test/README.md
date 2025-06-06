# Tests for ARO HCP E2E Setup

Simple set of tests for the scripts used in ARO HCP E2E Setup.
To execute these tests, run `make test` in the parent directory.

## How to read test failures?

When a test case fails, error is reported in diff format and no more test
cases are executed.

In the following example, test case "Cluster with nodepools" from
`test-aro-setup-metadata.sh` failed, because it expects given value to be
present, but the test got no value:

```
$ make test
test/test-arocurl.sh
Test: Create Request
Test: Get Request
Test: Passing Headers
test/test-aro-setup-metadata.sh
Test: Infra Only
Test: Cluster with nodepools
1c1
< { "value": "one" }
---
> null
make: *** [Makefile:11: test] Error 1
```

To understand what is wrong, check source of test check in given file (in out
case `test-aro-setup-metadata.sh`).
