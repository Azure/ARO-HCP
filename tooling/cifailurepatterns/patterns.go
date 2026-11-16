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

package cifailurepatterns

import (
	"regexp"
)

var (
	reProviderPath              = regexp.MustCompile(`/providers/(Microsoft\.[A-Za-z]+(?:\.[A-Za-z]+)?)/`)
	reProviderText              = regexp.MustCompile(`(Microsoft\.[A-Za-z]+(?:\.[A-Za-z]+)?)`)
	reCleanFmtWrap              = regexp.MustCompile(`<\*[^>]+\|\s*0x[0-9a-fA-F]+>\s*:?`)
	reCleanHexAddress           = regexp.MustCompile(`\b0x[0-9a-fA-F]+\b`)
	reCleanGoFileLine           = regexp.MustCompile(`\b\w[\w./-]*\.go:\d+\b`)
	reCleanUnexpectedPrefix     = regexp.MustCompile(`(?i)^\s*unexpected error:\s*`)
	reCleanWrapperPrefix        = regexp.MustCompile(`(?i)^\s*(msg:|err:|caused by:)\s*`)
	reCleanURL                  = regexp.MustCompile(`https?://[^\s"'<>]+`)
	reCleanSubscription         = regexp.MustCompile(`/subscriptions/[0-9a-fA-F-]+`)
	reCleanRedactedSubscription = regexp.MustCompile(`(?i)/subscriptions/X{20,}`)
	// Azure resource-ID path segment, e.g. .../resourcegroups/oidc-wi-2jj8pc/...
	// The generated resource-group name is instance-specific; normalize so the
	// same class of error merges across runs.
	reCleanResourceGroupsPathSegment = regexp.MustCompile(`(?i)/resourcegroups/[a-z0-9-]+/`)
	reCleanCosmosDBPathSegment       = regexp.MustCompile(`(?i)(/dbs/)arohcpci\d+-rp-j\d+(/colls/)`)
	// "The vault name '<name>' is already in use." — the Key Vault name embeds a
	// random suffix (and may contain hyphens), so it will not match the generic
	// 20+ char alnum opaque-ID regex below. Normalize explicitly so repeated
	// vault-name-collision failures merge into a single pattern.
	reCleanVaultNameAlreadyInUse = regexp.MustCompile(`(?i)the vault name '[^']+' is already in use\.?`)
	reCleanUUID                  = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F-]{27,}\b`)
	reCleanHexLong               = regexp.MustCompile(`\b[0-9a-fA-F]{32,}\b`)
	reCleanLookupHostOnServer    = regexp.MustCompile(`(?i)\blookup\s+[a-z0-9.-]+\s+on\s+\d{1,3}(?:\.\d{1,3}){3}:\d+\b`)
	reCleanResourceGroupQuoted   = regexp.MustCompile(`(?i)resourcegroup="[^"]+"`)
	reCleanResourceGroupBare     = regexp.MustCompile(`(?i)\bresource group [a-z0-9-]+\b`)
	reCleanResourceGroupSingle   = regexp.MustCompile(`(?i)\bresource group '[^']+'`)
	reCleanClusterQuoted         = regexp.MustCompile(`(?i)cluster="[^"]+"`)
	reCleanClusterPhraseQuoted   = regexp.MustCompile(`(?i)\bcluster "[^"]+"`)
	reCleanClusterCreationQuoted = regexp.MustCompile(`(?i)\bcluster creation ["'][^"']+["']`)
	reCleanForClusterPhrase      = regexp.MustCompile(`(?i)\bfor cluster [a-z0-9-]+\b`)
	reCleanInClusterPhrase       = regexp.MustCompile(`(?i)\bin cluster [a-z0-9-]+\b`)
	reCleanOnClusterPhrase       = regexp.MustCompile(`(?i)\bon cluster [a-z0-9-]+\b`)
	reCleanHCPClusterPhrase      = regexp.MustCompile(`(?i)\bhcp cluster [a-z0-9-]+\b`)
	// "cluster delayed-rbac-87tl668hqnnr/delayed-rbac-cluster entered terminal state"
	// The first path segment is a generated identity/resource-group name.
	reCleanClusterNamespaceName = regexp.MustCompile(`(?i)\bcluster [a-z0-9-]+/[a-z0-9-]+`)
	reCleanExternalAuthQuoted   = regexp.MustCompile(`(?i)external auth "[^"]+"`)
	reCleanExternalAuthBare     = regexp.MustCompile(`(?i)\bexternal auth [a-z0-9-]+\b`)
	reCleanVMQuoted             = regexp.MustCompile(`(?i)\bVM "[^"]+"`)
	// These identities are normalized only inside their explicit resource contracts.
	reCleanWildcardDNSRecord  = regexp.MustCompile(`(?i)(\bresource record '\*\.e2e-dns-)[bcdfghjklmnpqrstvwxz2456789]{6}(')`)
	reCleanAzureVMReference   = regexp.MustCompile(`(?i)/providers/Microsoft\.Compute/virtualMachines/[a-z0-9_.-]+`)
	reSwiftTimeout            = regexp.MustCompile(`(\bVerifySwiftRouterChecks\s+\S+\s+timed out after )\d+(?:h\d+)?(?:m\d+)?(?:\.\d+)?s:`)
	reSwiftScope              = regexp.MustCompile(`\b(?:dev|int|stg|prod)/[a-z0-9]+/ci[0-9]+-j[0-9]+-mgmt-[0-9]+/ocm-arohcp[a-z0-9-]+-cluster\b`)
	reSwiftNode               = regexp.MustCompile(`\bnode=aks-[a-z0-9]+-[0-9]+-vmss[0-9a-z]+\b`)
	reSwiftSummaryIdentity    = regexp.MustCompile(`\b(summaryUID|pass|at)=[^\s;]*`)
	reSwiftReplicaCount       = regexp.MustCompile(`\b(desired|current|rows)=[0-9]+\b`)
	reCleanPodQuoted          = regexp.MustCompile(`(?i)\bpods? "[^"]+"`)
	reCleanNamespaceQuoted    = regexp.MustCompile(`(?i)\bnamespaces? "[^"]+"`)
	reCleanServiceAccountPath = regexp.MustCompile(`(?i)\bservice account [^/\s]+/([a-z0-9-]+)`)
	reCleanNodePoolQuoted     = regexp.MustCompile(`(?i)nodepool="[^"]+"`)
	reCleanNodePoolAssignment = regexp.MustCompile(`(?i)nodePool=[a-z0-9-]+`)
	// Generated node-pool names contain a digit or hyphen. Do not match
	// English words such as "creation", "condition", or "status".
	reCleanNodePoolPhrase         = regexp.MustCompile(`(?i)\bnode pool (?:[a-z0-9]*-[a-z0-9-]+|[a-z0-9]*\d[a-z0-9-]*)\b`)
	reCleanNodePoolPhraseQuoted   = regexp.MustCompile(`(?i)\bnode pool "[^"]+"`)
	reCleanAgentPool              = regexp.MustCompile(`(?i)\bagent pool [a-z0-9-]+\b`)
	reBoolAssertionContext        = regexp.MustCompile(`(?is)Timed out after [0-9.]+s\.\s*\n(?P<context>[^\n]+)\s*\nExpected\s*\n\s*<bool>:\s*false\s*\n\s*to be true`)
	reAssertionErrorSignal        = regexp.MustCompile(`(?i)(error|failed|timeout|forbidden|denied|conflict|deadline|not found|invalid|http2:)`)
	reEventuallyWrapperLine       = regexp.MustCompile(`(?i)^the function passed to eventually failed at .+ with:?$`)
	reTimedOutAfterLine           = regexp.MustCompile(`(?i)^timed out after [0-9.]+s\.`)
	reCodeField                   = regexp.MustCompile(`"code"\s*:\s*"([A-Za-z0-9_]+)"`)
	reCauseBySplit                = regexp.MustCompile(`(?i)caused by:`)
	reErrorCode                   = regexp.MustCompile(`(?i)ERROR CODE:\s*([A-Za-z0-9_]+)`)
	reAzureMessage                = regexp.MustCompile(`(?is)"message"\s*:\s*"(.+?)"\s*[,}\]]`)
	reASCIILetter                 = regexp.MustCompile(`[A-Za-z]`)
	rePickErrorSignal             = regexp.MustCompile(`(?i)(error|failed|timeout|forbidden|denied|conflict|deadline|not found)`)
	reHTTPResponseStatusLine      = regexp.MustCompile(`(?i)^response [45][0-9]{2}:\s*.+$`)
	reHTTPRequestLine             = regexp.MustCompile(`(?i)^(GET|HEAD|POST|PUT|DELETE|CONNECT|OPTIONS|TRACE|PATCH)\s+https?://\S+$`)
	reRouteHostNeverFound         = regexp.MustCompile(`(?i)route host was never found:[^\n]+`)
	reClusterOperatorsUnavailable = regexp.MustCompile(`(?i)cluster operators not available:[^\n]+`)
	reRateLimiterDeadline         = regexp.MustCompile(`(?i)client rate limiter wait returned an error: context deadline exceeded`)
	// Ginkgo assertion header, e.g. `fail [file.go:191]: failed to poll admin
	// credential 1 to completion`. Normally excluded as wrapper noise, but used
	// as a last-resort fallback so a generic "context deadline exceeded" block
	// doesn't discard the only test-specific detail available (the message
	// following the file:line marker).
	reFailAssertionHeader = regexp.MustCompile(`(?i)^fail \[[^\]]*\]:\s*(.+)$`)
	// Step-progress polling noise: repeated "Waiting for X to finish (state:
	// Y, elapsed Ns)..." lines and DNS/service-readiness retry-loop attempt
	// lines. Both vary only by an elapsed-seconds/attempt counter and carry no
	// distinguishing failure signal, so they must never be picked as the
	// canonical evidence phrase.
	reWaitingToFinishProgressLine = regexp.MustCompile(`(?i)^waiting for [^)]+\(state:\s*[^,]+,\s*elapsed \d+s\)\.\.\.$`)
	reRetryAttemptProgressLine    = regexp.MustCompile(`(?i)\battempt \d+ failed \(elapsed \d+s/\d+s;[^)]*\);\s*retrying in \d+s\.\.\.$`)
	// Terminal "giving up after N attempt(s) / Xs (limit Ys)" summary line
	// that follows a retry loop; used as a last-resort stable canonical when
	// no other distinguishing error line is found.
	reGivingUpAfterAttempts           = regexp.MustCompile(`(?i)giving up after \d+ attempt\(s\) / \d+s \(limit \d+s\)`)
	reUnexpectedOnly                  = regexp.MustCompile(`(?i)unexpected error:?`)
	reDeserializationLiteral          = regexp.MustCompile(`(?i)Deserializa(?:ti|i)on Error:[^\n]+`)
	reDeserializationNoOutput         = regexp.MustCompile(`(?i)Deserializa(?:ti|i)on Error:\s*no output from command`)
	reDeserializationToken            = regexp.MustCompile(`(?i)deserializa(?:ti|i)on error`)
	reCommandErrorLine                = regexp.MustCompile(`(?im)^Command Error:\s*[^\n]+$`)
	reQuotaRequiredAvailable          = regexp.MustCompile(`(?i)\brequired\s+['"]?\d+['"]?\s*,\s*available\s+['"]?\d+['"]?\b`)
	reQuotaRequestedRemaining         = regexp.MustCompile(`(?i)\brequested\s+['"]?\d+['"]?\s*,\s*remaining\s+['"]?\d+['"]?\b`)
	reRetryAfterSeconds               = regexp.MustCompile(`(?i)\bplease try again after\s+['"]?\d+['"]?\s+seconds\b`)
	reTimeoutMinutesExceeded          = regexp.MustCompile(`(?i)timeout\s+'\d+(?:\.\d+)?'\s+minutes exceeded`)
	reOperationTimeout                = regexp.MustCompile(`(?i)timeout\s+'(?:\d+(?:\.\d+)?|<minutes>)'\s+minutes exceeded during ([A-Za-z0-9_]+)`)
	reTemplateLineColumn              = regexp.MustCompile(`(?i)\bat line '\d+' and column '\d+'`)
	reExpectedErrorButNil             = regexp.MustCompile(`(?i)expected an error to have occurred\.\s*got:\s*\n\s*<nil>`)
	reAzureErrorDescription           = regexp.MustCompile(`(?is)"error_description"\s*:\s*"([^"]+)"`)
	reClusterServiceState             = regexp.MustCompile(`(?i)\bstate is "([^"]+)"`)
	reClusterServiceClusterRef        = regexp.MustCompile(`(?i)/api/aro_hcp/v1alpha1/clusters/[a-z0-9]+`)
	reDeletionDispatchedAt            = regexp.MustCompile(`(?i)\s*\(deletion dispatched at [^)]+\)`)
	reLeadingResourceCount            = regexp.MustCompile(`^\d+\s+`)
	reInvalidTemplateParameter        = regexp.MustCompile(`(?i)deployment template validation failed:\s*'the value for the template parameter '([^']+)'.*?is not provided\.`)
	reDenyAssignmentAction            = regexp.MustCompile(`(?i)perform action '([^']+)'`)
	reNetworkAssociationError         = regexp.MustCompile(`(?i)error message:\s*(.+)$`)
	reUnavailableWorkload             = regexp.MustCompile(`(?i)^([a-z0-9-]+)(?: deployment| statefulset| daemonset)? has \d+ unavailable replicas?$`)
	reStampPrefix                     = regexp.MustCompile(`(?i)^stamp \d+(?::\s*|\s+)`)
	reServerPatchTimeout              = regexp.MustCompile(`(?i)the server was unable to return a response in the time allotted, but may still be processing the request \(patch configmaps ([^)]+)\)`)
	reGetPodsName                     = regexp.MustCompile(`(?i)\bget pods [a-z0-9-]+`)
	reNotReadyNodes                   = regexp.MustCompile(`(?i)(not ready nodes:\s*)\[[^\]]+\]`)
	reHostedClusterVersion            = regexp.MustCompile(`(?i)(hosted cluster control plane version not yet completed:\s*version\s+)\S+(\s+is\s+)`)
	reHostedClusterElapsed            = regexp.MustCompile(`(?i)(\bstarted\s+)\d+(?:h\d+m\d+s|h\d+m|h\d+s|h|m\d+s|m|s)(\s+ago\b)`)
	reDeletionResourceCount           = regexp.MustCompile(`\s*\(\d+\)`)
	reCleanKlusterletDeploymentQuoted = regexp.MustCompile(`(?i)(deployment=")klusterlet-[a-z0-9]{20,}-(.+?)(")`)
	reModelDiffSummary                = regexp.MustCompile(`(?i)operation result model did not match expected model[^\n]*`)
	reX509CertificateMismatch         = regexp.MustCompile(`(?i)tls: failed to verify certificate: x509: [^\n"]+`)
	reTimedOutAfterDuration           = regexp.MustCompile(`(?i)^timed out after [0-9]+(?:\.[0-9]+)?s\.$`)
	reTimedOutAssertionWrapper        = regexp.MustCompile(`(?i)^fail \[[^\]]*\]:\s*(timed out after [0-9]+(?:\.[0-9]+)?s\.)$`)
	rePlaceholderAssertionValue       = regexp.MustCompile(`^<[^>\n]+>:\s*\.{3}\s*$`)
	reStructuredFieldLine             = regexp.MustCompile(`^[+-]?\s*[A-Za-z_][A-Za-z0-9_]+:\s+.*(?:,\s*|\{\s*)$`)
	reLogfmtReleaseStatusDesc         = regexp.MustCompile(`(?i)level=info[^\n]*msg="determined release status\."[^\n]*description="((?:\\.|[^"])*)"`)
	reCleanupWorkflowTarget           = regexp.MustCompile(`(?i)(ordered cleanup workflow failed for )((?:[a-z0-9-]|<id>)+)(:)`)
	reCleanupWorkflowMethodURL        = regexp.MustCompile(`(?i):\s*(?:GET|POST|PUT|PATCH|DELETE)\s+<url>`)
	reCleanupWorkflowResourceName     = regexp.MustCompile(`(?i)(failed deleting )((?:[a-z0-9-]|<id>)+)( \([^)]+\):)`)

	// Dial-TCP address: normalize raw IPs left behind after URL masking.
	reCleanDialTCPAddress = regexp.MustCompile(`\bdial tcp \d{1,3}(?:\.\d{1,3}){3}:\d+\b`)
	// Istio/envoy "dialing <ip>:<port>" — same IP/port noise, different verb.
	reCleanDialingAddress = regexp.MustCompile(`\bdialing \d{1,3}(?:\.\d{1,3}){3}:\d+\b`)
	reCleanIP             = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d+)?\b`)
	reCleanBracketedIPv6  = regexp.MustCompile(`\[[0-9a-fA-F]*:[0-9a-fA-F:]+\](?::\d+)?`)
	reCleanISOTimestamp   = regexp.MustCompile(`\b20[0-9]{2}-[0-9]{2}-[0-9]{2}T[0-9:.]+Z\b`)
	reCleanHumanTimestamp = regexp.MustCompile(`\b\d{1,2}/\d{1,2}/20\d{2} \d{1,2}:\d{2}:\d{2} (?:AM|PM)\b`)
	reCleanResolveWithin  = regexp.MustCompile(`(?i)\bdid not resolve within [0-9]+(?:h[0-9]+m)?(?:m[0-9]+s|s)\b`)
	reCleanRouteHost      = regexp.MustCompile(`(?i)\bagnhost-e2e-sample-app-[a-z0-9]+\.apps\.[a-z0-9.-]+\b`)
	reCleanBreakglassPath = regexp.MustCompile(`(?i)/hcpopenshiftclusters/[^/]+/breakglass/[^/]+/kubeconfig`)
	reCleanRedactedQuoted = regexp.MustCompile(`"X{20,}"`)
	reCleanFailEmpty      = regexp.MustCompile(`(?i)^\s*fail\s*\[\s*\]:\s*`)
	reCleanSelector       = regexp.MustCompile(`(?i)\bselector "[^"]+"`)
	reExpectedReadyNodes  = regexp.MustCompile(`(?i)\bexpected \d+ ready \(and schedulable\) nodes\b[^,]*,\s*found \d+\b`)
	// Logfmt-style timestamp (e.g. time=2026-04-17T11:04:19.211Z).
	reCleanLogfmtTimestamp = regexp.MustCompile(`\btime=[0-9]{4}-[0-9]{2}-[0-9]{2}T[A-Z0-9:.]+\s*`)
	// JSON "time" field with ISO-8601 value (e.g. prow entrypoint logs).
	reCleanJSONTimeField = regexp.MustCompile(`"time"\s*:\s*"[0-9]{4}-[0-9]{2}-[0-9]{2}T[^"]*",?\s*`)
	// Prow entrypoint single-line JSON: extract just the msg value.
	reProwEntrypointMsg = regexp.MustCompile(`"component"\s*:\s*"entrypoint"[^}]{0,400}"msg"\s*:\s*"([^"]+)"`)
	// All CreateHCPCluster*FromParam / *AndWait helper variants.
	reCreateHCPClusterTimeout = regexp.MustCompile(`(?i)createhcpcluster\w*(?:fromparam|andwait)`)
	// Gomega "Expected success, but got an error:" followed by optional type
	// wrapper line, then the complete error detail line, including periods.
	reGomegaSuccessFailure = regexp.MustCompile(`(?i)Expected success, but got an error:\s*\n(?:[ \t]*<[^>\n]*>[ \t]*:?[ \t]*\n)?[ \t]*([^\n]+)`)
	// HCP API / reserved hostnames (e.g. api.<cluster>.<stamp>.<region>.aroapp-hcp.io).
	// These appear in x509 certificate-mismatch error text and are cluster/stamp-
	// specific; normalize so the same class of cert error merges across clusters.
	reCleanHCPApiHost = regexp.MustCompile(`(?i)\b(?:api\.[a-z0-9][a-z0-9.-]*|reserved(?:\.[a-z0-9][a-z0-9.-]*)?)\.aroapp-hcp(?:\.azure-test\.net|\.io)\b`)
	// OCP version strings like openshift-v4.22.0-candidate (the version number
	// is instance-specific; normalize so the same class of error merges).
	reCleanOCPVersion = regexp.MustCompile(`\bopenshift-v[0-9]+\.[0-9]+\.[0-9]+-[a-z]+\b`)
	// Single-quoted opaque alphanumeric IDs (≥20 chars) such as OCM cluster
	// internal IDs that appear in Azure RP error messages.
	reCleanQuotedOpaqueID = regexp.MustCompile(`'[a-z0-9]{20,}'`)
	// Single-quoted Azure provider/type/name path, e.g.
	// 'Microsoft.ContainerService/managedClusters/prow-j123456-mgmt-1'. Keep the
	// provider/type anchor but scrub the generated resource name.
	reCleanQuotedAzureResourcePath = regexp.MustCompile(`'Microsoft\.[A-Za-z]+(?:\.[A-Za-z]+)?(?:/[A-Za-z0-9._-]+){2,}'`)
	// logfmt err= field value extracted from a step-error log line.
	// The pattern matches level=error … msg="Step errored." … err="<value>" to
	// capture the actionable error message without logfmt boilerplate fields.
	reLogfmtStepErroredErr = regexp.MustCompile(`(?i)level=error[^"]*msg="step errored\."[^"]*err="([^"]+)"`)

	// Gomega type-annotation line: <*errors.errorString | 0x...>: or <wait.errInterrupted>:
	// Used by bestGomegaInnerError to locate the actual error message that
	// follows the type wrapper in HaveOccurred() assertion output.
	reGomegaTypeAnnotation = regexp.MustCompile(`^\s*<[^>]+>\s*:?\s*$`)

	// Kubernetes machine/node name: 12+ char alphanumeric prefix followed by
	// 3+ dash-separated segments and a 4-6 char hash suffix. These are
	// instance-specific and must be scrubbed to merge the same failure class.
	reCleanK8sNodeName = regexp.MustCompile(`\b[a-z0-9]{12,}(?:-[a-z0-9]+){3,}-[a-z0-9]{4,6}\b`)

	// OCP upgrade-graph channel references (e.g. stable-5.2, candidate-4.20).
	// The version is instance-specific; normalize so all version-parametrized
	// variants of the same channel-lookup failure merge.
	reCleanOCPChannel = regexp.MustCompile(`\b(?:stable|candidate|fast|eus)-[0-9]+\.[0-9]+\b`)

	// Kubernetes klog / structured-log prefix: E<MMDD> <HH:MM:SS>.<us> <goroutine>
	// The file:line portion is already stripped by reCleanGoFileLine; this
	// strips the remaining severity+timestamp+goroutine token so the real
	// message (e.g. "Unhandled Error" err=…) becomes the canonical phrase.
	reCleanK8sLogPrefix = regexp.MustCompile(`[EWI][0-9]{4} [0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]+ +[0-9]+\s*\]?`)

	// Bare nodepool name in the form "nodepool <name>" (single token, no quotes)
	// that appears in UpdateNodePoolAndWait / timeout messages. The quoted form
	// nodepool="<name>" is handled by reCleanNodePoolQuoted above; this pattern
	// covers the unquoted counterpart so the same failure class merges.
	// Require a digit or hyphen so "NodePool status" is not treated as a name.
	reCleanNodePoolBare = regexp.MustCompile(`(?i)\bnodepool (?:[a-z0-9]*-[a-z0-9-]+|[a-z0-9]*\d[a-z0-9-]*)\b`)
	// Hypershift Machine object dumps: "Machine <generated-name>: Reason: ..."
	reCleanMachinePhrase = regexp.MustCompile(`(?i)\bMachine [A-Za-z0-9][A-Za-z0-9._-]*:`)
	// Repeated "version X is Partial (want Completed), started Y ago" clauses
	// after the first normalized control-plane version line.
	reExtraPartialVersionClause = regexp.MustCompile(`(?i)(?:;\s*)+version \S+ is Partial \(want Completed\), started \S+ ago`)
	// Nightly payload strings like 4.20.0-0.nightly-multi-2026-09-30-014938.
	reCleanOCPNightly = regexp.MustCompile(`\b\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)*-0\.nightly(?:-[A-Za-z0-9]+)*-\d{4}-\d{2}-\d{2}-\d+\b`)
	// Cluster-local HCP DNS names that are not covered by reCleanHCPApiHost.
	reCleanHCPOsadevHost = regexp.MustCompile(`(?i)\b(?:[a-z0-9-]+\.)*hcp\.osadev\.cloud\b`)
	// Image-mirror CI namespace + ephemeral oras auth dirs.
	reCleanCIOpID                   = regexp.MustCompile(`\bci-op-[a-z0-9]+\b`)
	reCleanTmpTmpDir                = regexp.MustCompile(`/tmp/tmp\.[A-Za-z0-9]+`)
	reNodePoolReplicasWant          = regexp.MustCompile(`(?i)replicas is \d+, want \d+`)
	reNodePoolReplicasWantAutoscale = regexp.MustCompile(`(?i)replicas is \d+, want >= \d+ \(autoscaling min\)`)
	reNodePoolMachineCounts         = regexp.MustCompile(`(?i)\b\d+ of \d+ machines\b`)

	// "make[N]: Entering/Leaving directory '...'" lines emitted by GNU Make
	// when shell steps run sub-makes. These are build preamble noise that
	// precede the real error in multi-line err= log fields; strip them so the
	// canonical phrase reflects the actual failure rather than the banner.
	reCleanMakeDirectory = regexp.MustCompile(`(?i)make\[\d+\]: (?:Entering|Leaving) directory\s+'[^']*'\s*\.?\s*`)
)
