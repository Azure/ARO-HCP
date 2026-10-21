
# Why do we have API Reviews and Conventions?

We are creating a product where the API and its design play an important part in how our users interact with the product.
Clients will get their code or workflows functioning once and will then expect those same workflows to continue to function forever.
ARM provides good requirements and guidance, but sometimes it's less clear how to achieve those requirements in the code that must actuate them.
This document is starting in response to a few common questions we've received.

# Adding stricter validation to a field

We are discovering cases where we have validation that must be made more restrictive, the reasons why vary and aren't pertinent to how we handle it.

*Critical Requirement*: If there is stricter validation for field/A and a user modifies field/B and NOT field/A, then the validation for field/A must NOT fail.
This allows unrelated field modification to succeed but requires new instances of field/A and any modification of field/A to conform to the new validation.
Once we have confirmed that all instances of invalid data are gone from the data in integration, stage, and prod, we can make the field/A validation unconditional.

# Why have union discriminators?

Sometimes we have a field that may someday have more than one logical option, but we only have one option to start.
It is tempting to avoid adding a field that can only currently have one value, but that makes it impossible for older clients to react properly.
This is more obvious with an example.

Consider an old client with an old API version. The field doesn't exist.
1. v2 client creates an instance with newValue for discriminator and other values set
2. v1 client reads the instance and interprets "other values" as though the discriminator was "oldValue".
3. This leads to misunderstanding the configuration and potentially taking improper action, warnings, or security decisions.

Contrast that with an approach where the discriminator exists, but only has one value
1. v2 client creates an instance with newValue for discriminator and other values set
2. v1 client reads the instance and reads the discriminator and realizes it doesn't know what the "other values" mean.
3. At this point the v1 can fail safely.

# API version testing

Our E2E API version testing strategy is intentionally conservative to preserve stability while still validating new API surface as it is introduced.

The core principles are:

1. Existing E2E tests are not retrofitted to new API versions.
2. New API versions are validated through new E2E tests created for new features and behavior.

## Existing E2E tests remain on their current API version

When an E2E test already validates an existing scenario, we do not update it just to call a newer API version. Those tests represent stable customer workflows, and keeping them on their original API version provides ongoing regression coverage for compatibility.

This approach avoids churn in mature test cases and helps us detect accidental behavior drift in older supported API versions. It also keeps historical signal intact, because test failures can be interpreted as product regressions rather than test migration side effects.

## New API versions are covered by new feature tests

When we add a new API version, we add or extend E2E coverage through test cases that validate the new features or behavior introduced in that version. In practice, this means new version-specific functionality should arrive together with new E2E scenarios that exercise it end-to-end.

This gives us confidence that:

- previously supported behavior remains validated by existing tests
- newly introduced behavior is validated by targeted tests using the new API version

Together, these two layers provide compatibility coverage for older clients and correctness coverage for new capabilities without requiring broad rewrites of the existing E2E suite.

There is no specific set of test helper methods that a new API version should introduce, only those that are necessary for testing new features.
