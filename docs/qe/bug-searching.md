# ARO HCP Bug Searching

This document explains how to search for existing bugs in ARO HCP project.

## Where to search for bugs?

The primary bug tracker for ARO HCP is in the [ARO project, component
`ARO HCP`, type
`Bug`](https://redhat.atlassian.net/issues?jql=project%20%3D%20ARO%20and%20component%20%3D%20%22ARO%20HCP%22%20and%20type%20%3D%20bug).

One can also search for related CI and infra issues in
[AROSLSRE project, type `Bug`](https://redhat.atlassian.net/issues?jql=project%20%3D%20AROSLSRE%20and%20type%20%3D%20bug&wildcardFlag=true).

## How to search for a bug

1) Identify representative part of the error message you are getting (eg. what
   you get from the test case error in a failed E2E job run),

2) Try to search for this error message in ARO and/or AROSLSRE projects:


   ```
   ((project = ARO and component = "ARO HCP") or (project = AROSLSRE)) and type = bug and status != Closed and description ~ "ERROR_MSG"
   ```

   Where `ERROR_MSG` is the error you are looking for.

3) Try to search for the error in OpenShift CI results of ARO-HCP job:

   <https://search.dptools.openshift.org/?search=ERR_MSG&maxAge=336h&context=1&type=build-log&name=ARO-HCP&excludeName=&maxMatches=5&maxBytes=20971520&groupBy=job>

4) In case you can't find any results, or you are getting too many of them, try
   to tweak the error message you are searching for.

5) In case you find the error message in results of prow jobs with `branch-ci`
   prefix, you can try to navigate to the *snapshot analysis* of the run to
   get more context.

## Advanced CI result search

### Sippy

The CI results are available in Sippy:

- [sippy job health for aro-integration](https://sippy-auth.dptools.openshift.org/sippy-ng/jobs/aro-integration)
- [sippy job health for aro-stage](https://sippy-auth.dptools.openshift.org/sippy-ng/jobs/aro-stage)
- [sippy job health for aro-production](https://sippy-auth.dptools.openshift.org/sippy-ng/jobs/aro-production)

In case you suspect that an issue is reproduced by a particular test case, you
can filter a test result for this test case only across job runs via "job runs"
page:

- e.g.: [All job runs aro-integration](https://sippy-auth.dptools.openshift.org/sippy-ng/jobs/aro-integration/runs?filters=%7B%22items%22%3A%5B%7B%22field%22%3A%22name%22%2C%22operator%22%3A%22equals%22%2C%22value%22%3A%22branch-ci-Azure-ARO-HCP-main-e2e-integration-e2e-parallel%22%7D%5D%7D&sortField=timestamp&sort=desc) is a list of job runs for ARO HCP tests in INT, and it can be further filtered

### CI Health Dashboard

[CI Health Dashboard](https://cihealth.tools.hcpsvc.osadev.cloud/) provides an
option to review Failure Patterns across environments. Results can be exported
into json.

### ARO Release Dashboard

[ARO Release Dashboard](https://releases.dev.aro.azure-test.net/) provides an
option to see E2E results and snapshot analysis for gating job runs.
