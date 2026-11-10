# Operatarr intake and improvement roadmap

This document records the repository intake and sequences follow-up work. It
reflects the current implementation, not the broader ARR-stack vision: the
repository currently implements Sonarr and MediaLibrary resources.

## Current strengths

- The project follows familiar Go, controller-runtime, and Kubebuilder
  conventions, with separate API, controller, jobrunner, and deployment areas.
- Controllers use owner references and `CreateOrPatch` for managed resources.
- The manager detects Gateway API availability and conditionally registers an
  HTTPRoute watch.
- Managed application and initialization containers already apply several
  useful security settings, including non-root execution and dropped
  capabilities.
- GitHub Actions runs build and lint checks. `make test` provisions its pinned
  envtest assets, and the controller test suite uses the configured envtest
  assets instead of a platform-specific path. Cluster e2e tests are separate.
- The scheduled Dev Container lockfile workflow refreshes pinned feature
  versions and digests.

## Main findings

### Reconciliation correctness

- Optional Sonarr pod security context and MediaLibrary permission fields are
  dereferenced by controller code without nil handling. API-valid resources
  can therefore panic during reconciliation.
- The MediaLibrary controller creates an initialization Job but does not watch
  Jobs. A completed Job may not trigger a reconcile to update MediaLibrary
  status. Sonarr also references a MediaLibrary without a watch on that
  dependency.
- Reconciliation status needs consistent semantics. For example, a missing
  pre-existing PVC currently marks a MediaLibrary initialized even though it
  cannot be used.
- Condition merging only replaces an existing condition when its status
  changes; reason and message updates with unchanged status may be lost.

### Storage and integration

- The Sonarr StatefulSet uses a placeholder PVC claim name, and the Sonarr
  media volume path is unfinished.
- The current PersistentVolume reconciliation does not define a backing volume
  source. The intended storage design, cross-namespace model, and deletion
  behavior need to be decided before implementation.
- HTTPRoute is enabled by default, while reconciliation fails if Gateway API is
  unavailable. This makes an optional integration a blocker for other Sonarr
  resources.

### API, documentation, and product scope

- MediaLibrary PVC validation markers do not currently produce the intended
  validation in the generated CRD; one expression refers to `resources` while
  the API field is `size`.
- MediaLibrary printer columns refer to status paths that do not match the
  implemented status fields, and the size column has an unsuitable type.
- The README feature, installation, and usage sections and both resource
  samples remain placeholders.
- Additional ARR applications are aspirations, not currently implemented
  resources.

## Prioritized plan

### 0. Test and CI foundation — complete

- [x] Make CI run the Makefile test target, which provisions envtest assets.
- [x] Avoid hardcoded OS/architecture-specific envtest asset paths.
- [x] Keep cluster-dependent e2e tests outside the normal controller test run.
- [x] Select the latest published envtest assets within Kubernetes 1.35; the
  former 1.35.10 patch pin was not available from the asset index.

### 1. Reconciliation reliability — in progress

- [x] Safely handle omitted optional Sonarr security-context and MediaLibrary
  permission fields. Preserve the documented permission defaults where
  possible.
- [x] Add focused tests for omitted permission fields and default values.
- [ ] Add focused tests for the remaining status and error paths.
- [x] Observe initialization Job changes and reconcile Sonarr when its
  referenced MediaLibrary changes.
- [ ] Define retry behavior and conditions for resources waiting on dependencies.
- [ ] Make Gateway API availability non-blocking for Sonarr features that do not
  require HTTPRoute.

**RBAC approval:** Approved for manager `batch/jobs` permissions limited to
`create`, `get`, `list`, and `watch`, required by initialization and Job watches.

### 2. Storage design and completion

- [ ] Agree on ownership, namespace access, mounting, PV/PVC behavior, and
  cleanup semantics.
- [ ] Replace the placeholder Sonarr media claim with the selected MediaLibrary
  PVC integration.
- [ ] Reconcile PVs only if the approved storage model requires them, including
  the correct backing source.
- [ ] Test the supported access modes and namespace cases.

**Approval gate:** Obtain approval before changing storage behavior or deletion
policy.

### 3. Status and API correctness

- [ ] Ensure initialization and readiness status never report success for
  unusable dependencies.
- [ ] Update existing conditions when their reason or message changes, while
  preserving transition-time semantics.
- [ ] Correct MediaLibrary PVC validation and printer columns.
- [ ] Report observed generation consistently where appropriate.

**Approval gate:** Obtain explicit approval before editing API types, markers,
or generated CRD schema. Regenerate manifests only after approval.

### 4. Usability and docs

- [ ] Replace README placeholders with supported features, install instructions,
  prerequisites, and a working usage path.
- [ ] Replace placeholder samples with valid MediaLibrary and Sonarr examples.
- [ ] Document Gateway API and storage requirements, including limitations.
- [ ] Clearly separate implemented resources from future product scope.

### 5. Architecture and product expansion

- [ ] Reassess common controller behavior after correctness work; extract only
  genuinely shared logic.
- [ ] Keep reconciliation steps explicit and ordered by dependency; avoid a
  generic controller framework without demonstrated duplication.
- [ ] Decide whether to add other ARR applications after the existing
  Sonarr/MediaLibrary path is reliable and documented.

## Working and validation rules

- Work top-down by phase; keep changes focused and update this checklist as
  work lands.
- Preserve per-reconcile state, idempotency, ownership, error propagation, and
  retry behavior.
- Do not change CRD schemas, expand RBAC, or alter storage/deletion behavior
  without the approvals listed above.
- For Go changes, run focused tests, then `make vet` and `go build ./...`.
  Controller or API changes also require the controller test suite.
- Run e2e tests only against an explicitly approved test cluster. The ordinary
  test target must not depend on a live cluster.
