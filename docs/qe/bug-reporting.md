# ARO HCP Bug Reporting and Tracking

This document explains how the ARO HCP project tracks bugs and how a bug
related to ARO HCP should be reported.

## Where to report bugs?

The primary bug tracker for ARO HCP is in the [ARO project, component
`ARO HCP`, type
`Bug`](https://redhat.atlassian.net/issues?jql=project%20%3D%20ARO%20and%20component%20%3D%20%22ARO%20HCP%22%20and%20type%20%3D%20bug).
Every ARO HCP bug should be reported there no matter which component(s) is
suspected to be affected.

## What to do before reporting a bug?

To avoid duplication, try to search for already reported bugs in ARO and
AROSLSRE projects which could cover the problem, as explained in
[bug-searching.md](bug-searching.md) document.

## What to include in the bug report?

The text of a bug report should include the following sections:

- Description of the problem: short, executive summary of the problem
- Version/Environment: note down:
    - version of the ARO HCP service (if you have access to this information)
    - version of OpenShift specified for hosted cluster(s)
    - if a testing environment (as explained in
      [ARO HCP Tenants and Environments](../environments.md)) was used,
      list the name of the environment and the subscription used, otherwise
      specify at least a region used
- How reproducible: eg. 1/10 means that the problem happened once out of 10
  retries
- Steps to reproduce: describe the use case in a list of steps, eg.:
   - deploy a hosted cluster with private load balancer and a node pool
   - scale up the node pool
- Actual Results: what did happen (operation didn't finish in 10 minutes and
  timed out)
- Expected Results (eg. deployment finished within 10 minutes)
- ARO HCP E2E Error (only in case of E2E test case failure)
- Additional Info: any references which could help with debugging or additional
  context

This format allows triage wrt end user impact of a bug.

## When should a JIRA be created for E2E test failure?

A bug should exist for each independent root failure. Since it can be hard to
determine if multiple failures across different runs are caused by the same
root failure, we should err on the side of caution and create JIRAs until it is
proven that the cause of failure is being investigated in another JIRA.

Since we have random tests at the moment, which are failing due to timeouts, a
new JIRA will not be created for each timeout, unless a specific test is
failing more than 3 times consecutively in particular E2E parallel suite.

## What to include in the bug report if reporting a new E2E test CI failure?

* JIRA title should be prefixed with `E2E Failure:  <TITLE OF BUG>`
* Labels:
  * `ENV_INT` or `ENV_STAGE` or `ENV_PROD` (based on the environment the
    failure was observed, attach all applicable labels)
  * `E2E_Failure`
* In "Steps to Reproduce" section, explain which test case was executed, at
  which point the test failed, and link to the test case source code on github,
  pointing out to the line where the error was reported. The link should use
  the commit id which was used to execute the test (if this information is
  provided in the test run, otherwise use latest main commit, and note it down
  in the report).
  * example:
    [mise\_routing.go:89](https://github.com/Azure/ARO-HCP/blob/0155214a43a3c85b395fc30a00c2abe2fa2eb1a9/test/e2e/mise_routing.go#L89)
  * one should check if the test case was changed recently, and note it down
    if this is the case
* In "ARO HCP E2E Error" section, the error from the test failure as provided
  in junit XML report in full
* In "Version/Environment", link:
   - to the corresponding prow job run with the failure
   - OpenShift CI Search or Sippy link for the frequency chart:
       * OpenShift CI Search for an unique error excerpt identifying the failure, [example](https://search.dptools.openshift.org/?search=timeout+%2710.000000%27+minutes+exceeded+during+GetAdminRESTConfigForHCPCluster&maxAge=336h&context=1&type=build-log&name=ARO-HCP&excludeName=&maxMatches=5&maxBytes=20971520&groupBy=job)
       * Sippy search filtering results of a particular test case for given set of jobs, [example of results of MISE tests in prod jobs](https://sippy-auth.dptools.openshift.org/sippy-ng/jobs/aro-production/runs?filters=%257B%2522items%2522%253A%255B%257B%2522columnField%2522%253A%2522ran_test_names%2522%252C%2522operatorValue%2522%253A%2522has%2520entry%2522%252C%2522value%2522%253A%2522MISE%2520Routing%2520routes%2520to%2520the%2520correct%2520frontend%2520based%2520on%2520version%2520header%2520MISE%2520v2%2520when%2520x-ms-mise-version%2520header%2520is%2520set%2522%257D%255D%252C%2522linkOperator%2522%253A%2522and%2522%257D)
* Evaluate for a clear regression, and propose revert (assuming this is in INT
  Stage), if:
  * test case was not changed recently
  * there is a history of this test passing until now
  * test fails because a clear error is coming from ARO HCP service operation
* Where possible, details on initial investigation to determine root cause.
  * Use and link analysis from ARO HCP dashboard (if the failure is from a
    rollout prow job)
  * The Kusto queries in each CI job should also help here

## Bug Taxonomy

When reporting new bug, follow [bug-taxonomy.md](bug-taxonomy.md).

## What if the root cause is elsewhere?

When we are sufficiently sure that a new bug is in a particular ARO HCP
dependency outside of ARO-HCP repository (eg. Hypershift Operator), a bug for
that component dependency should be created in the bug tracker for the
component directly, and this new bug should be linked to the bug in the ARO
project (which will become a tracking bug for the ARO side):

- Report bug in `ARO` project, component `ARO HCP`, type `Bug`.
- If we identify a component(s) where the problem needs to be addressed, report
  a bug there. Eg. for Hypershift Operator it is `OCPBUGS` project, component
  `HyperShift`, type `Bug`. See a table below for references to other
  components.
- Link the component bug as a blocker for the ARO bug:
  - Open the ARO bug and navigate to the "Issue Links"
  - Use the plus sign to add "is blocked by" link to the component Bug.

This means that we mandate *tracker bugs in the ARO project* for any bug
affecting ARO HCP. This is because each JIRA project may define a slightly
different bug tracking process, and we need a way to track bugs affecting ARO
HCP across any Red Hat project in JIRA.

### ARO HCP dependency component reference

Where to look when reporting or searching for bugs in ARO HCP components:

| ARO HCP component | JIRA query | JIRA project | JIRA component | JIRA type |
| :---- | :---- | :---- | :---- | :---- |
| HyperShift Operator | [`bugs`](https://issues.redhat.com/issues/?filter=12444057) | `OCPBUGS` | `HyperShift` | `Bug` |
| External DNS Operator | [`bugs`](https://issues.redhat.com/issues/?filter=12444059) | `NE` | `-` | `Bug` |
| Any other OpenShift component | [`bugs`](https://issues.redhat.com/issues/?jql=project%20%3D%20OCPBUGS) | `OCPBUGS` | `?` | `Bug` |

Note special cases for:

- Value `?` in the table means that one needs to pick a right value for the particular case.
- Value `-` means that there is no component defined in the project.
