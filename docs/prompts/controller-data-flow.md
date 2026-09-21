# Regenerate the controller and resource flow reference

Work from the current checkout of ARO-HCP. Regenerate `docs/cosmos-data-flow.md`
and the Graphviz sources and PNGs in `docs/diagrams/controller-flows/`.
The historical Markdown filename is retained for existing links; the reference
covers controller behavior, external effects, and resource lifecycles, not only
Cosmos DB. This prompt is maintained separately from the generated reference.

## Establish scope and evidence

1. Read repository and applicable directory guidance. Record the source revision
   and scope in the reference. Do not import controllers from another branch.
2. Inventory **every concrete controller**, including read-only observers,
   metrics, dumps, caches, migration/repair, garbage collection, examples,
   dynamically instantiated validations, and optional/legacy controllers.
   Follow constructors through application startup to the actual `Run` calls;
   distinguish instantiated controllers from reusable wrappers and helper loops.
3. Cover backend, fleet, kube-applier, mgmt-agent, and sessiongate. Include shared
   infrastructure controllers such as union kube-applier informers. Explain
   wrappers once; do not count each generic wrapper as a business controller.
   Include external components (Cluster Service, Maestro/work-agent, HyperShift,
   Kubernetes controllers, Azure) as boundary nodes, not as in-repo controllers.
4. Read implementations and delegated helpers, not just comments or names.
   Cross-check each summary and dependency against the actual guards, calls,
   writes, and completion observations. Distinguish registered, feature-gated,
   legacy, and example paths. Never infer causality from startup order.

Useful entry points (discover new ones rather than treating this as a closed list):

- `frontend/pkg/frontend/`, `admin/server/`
- `backend/pkg/app/backend.go`, `backend/pkg/controllers/`,
  `backend/pkg/azure/cachedreader/`, `backend/pkg/utils/validationutils/`
- `fleet/pkg/manager/manager.go`, `fleet/pkg/controllers/`
- `kube-applier/pkg/app/`, `kube-applier/pkg/controllers/`
- `mgmt-agent/cmd/`, `mgmt-agent/pkg/controller/`
- `sessiongate/pkg/controller/` and its startup wiring
- `internal/controllerutils/`, `backend/pkg/utils/controllerutils/`,
  `internal/database/unioninformers/`
- `internal/api/{coreapi,fleetapi,kubeapplierapi,metadataapi}/`,
  `internal/apihelpers/{coreapihelpers,fleetapihelpers,kubeapplierapihelpers,metadataapihelpers}/`,
  `internal/database/{cosmosstorage,informers,listers}/`
- Azure client interfaces, OCM/Cluster Service adapters, desire builders,
  validation implementations, and lifecycle tests called by these controllers.

## Required Markdown sections

### 1. Endpoint writes and external actions

For each mutating frontend endpoint, retain method/path, handler/source, objects
and important fields created/replaced/deleted, transactional versus standalone
writes, operation records, and downstream intent. Include relevant admin actions.
Do not describe multiple Cosmos containers as one: distinguish Resources,
Billing, Fleet, and per-management-cluster kube-applier storage.

Preserve the Cosmos request/RU attribution reference: policy wiring, source labels,
informer versus controller attribution, and the distinction between request counts
and charged RUs. These are shared instrumentation, not additional controllers.

### 2. Complete controller catalog

Organize by service, resource type, and responsibility so readers can find one
controller without following a giant graph. Give every concrete controller its
own entry, including each dynamically named validation and metrics controller.
Use the runtime name constant or literal (identify implementations without one).
Include source links and startup links. For every entry summarize:

- Trigger: informer(s), key scope, resync/cooldown, and explicit delayed retries.
  Distinguish a periodic resync from error retry and external-operation polling.
- Gate: actual `NeedsWork`, `ShouldProcess`, inline checks, mode/feature flags,
  and deletion behavior. State important predicates with field names and values.
- Inputs: relevant objects/fields, whether cached or live, and external reads.
- Effects: exact important Cosmos fields and external resources affected;
  distinguish create, update/patch, delete, observe, and log/metric-only actions.
- Completion: what confirms work is done, what remains pending, and who reacts.

Document inherited `Controller` status writes separately from domain writes.
“Read-only” must be scoped to the domain: a wrapper may still persist reconcile
status. Do not omit controllers just because they have no Cosmos writes.

Provide a separate external-effects table, especially for Azure resource groups,
role assignments, deny assignments, VM/network reads, workspace limits, Cluster
Service API mutations, and Kubernetes objects. State which component actually
performs the mutation, which controller only records intent, which controller
observes completion, and any read-only mode, eventual-consistency loop, or
unfinished cleanup. Never equate deleting an ApplyDesire document with deleting
its Kubernetes object; inspect the desire type and deletion code.

### 3. Resource lifecycle digraphs

Replace ASCII diagrams with embedded PNGs linked to their Graphviz DOT sources.
Produce create, update, and delete views for typical **cluster**, **node pool**,
and **external auth** instances (nine focused diagrams). Add focused views for
credentials, capacity/placement, ordered teardown, or other flows when they improve
understanding. Verify placement failure/deadline semantics, fleet readiness inputs,
and delete-dispatch prerequisites against the current source.

- These are directed graphs, **not necessarily DAGs**. Preserve real reconcile,
  retry, observation, and feedback cycles rather than inventing a linear pipeline.
- A node is a named controller, frontend action, external system, or observed
  state. Do not imply every instance passes through diagnostics or optional paths.
- A directed edge means a verified field write, external effect, or observation
  can enable/retrigger the target. Label it with the field or condition involved.
  If several incoming prerequisites are all required, mark that explicitly.
- Distinguish solid causal edges from dashed observation/reconcile feedback and
  optional paths. Explain the legend and success/failure/deletion semantics.
- Show the terminal ARM result and the asynchronous external work separately.
  Include wait/retry loops and conditional paths without claiming a strict order
  among independently scheduled controllers.
- Separate business flows from background controllers; summarize background
  work in the catalog rather than forcing it into each instance's lifecycle.
- Identify external boundaries and direct versus indirect Azure/Kubernetes
  writes. Avoid arrows that merely mean “these controllers run in the same app.”
- Split busy diagrams into focused panels. Use readable node labels, a consistent
  palette, generous spacing, and limited edge crossings. Keep graphs usable at
  their native resolution; link the full PNG and DOT next to each embedded view.
- Put source links and key edge evidence in adjacent captions/tables so causal
  claims can be checked without relying on image text alone.

Use Graphviz `dot` to render real PNG files. Commit both DOT and PNG artifacts and
provide a repeatable rendering command/script. Do not substitute Mermaid, ASCII,
placeholder images, or an image-generation model for the requested artifacts.

### 4. Shared fields and ownership

List fields written by multiple actors, including desired versus observed state,
operations, credentials, placement/capacity, and reconcile conditions. Include
single-writer fields when they are important downstream gates. Summarize external
resource ownership too: who creates, who updates, who deletes, and who only observes.
Link actors to source/catalog entries. Do not conflate API desired state with
confirmed Azure/Kubernetes existence.

## Verification and delivery

- Reconcile the catalog against controller constructors and startup registrations;
  report the covered services and concrete counts, and explain exclusions.
- Verify each controller source link and Markdown/PNG/DOT link exists. Replace
  stale paths after package moves. Use repository-relative links.
- Check that all nine primary lifecycle DOTs parse and render, their PNGs are
  nonempty, and the Markdown embeds every generated primary graph.
- Visually inspect every image (contact sheets plus full-size views where needed).
  Fix clipped labels, overlapping edges, unreadable text, and unnecessarily large
  canvases. Cycles are valid; do not remove them to make layout easier.
- Review gate edges against source, especially create preconditions, resource
  cleanup sequencing, and operation completion. Label uncertain or intentionally
  omitted details; do not invent a sequence.
- Keep this prompt in `docs/prompts/controller-data-flow.md`; link it from the
  reference and update any repository guidance that points to the old inline prompt.
- Regeneration is a documentation task. Do not change runtime behavior or contact
  live Azure/Kubernetes resources to manufacture evidence.
