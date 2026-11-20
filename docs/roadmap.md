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
  handled safely, with documented permission defaults applied when omitted.
- MediaLibrary initialization Jobs and referenced MediaLibrary changes enqueue
  reconciliations so status and dependent resources can catch up.
- Missing or invalid pre-existing PVCs keep MediaLibrary uninitialized and
  report an unavailable condition; dependency errors use controller-runtime's
  rate-limited retry behavior.
- Sonarr reports unavailable dependencies and reconciliation failures in its
  status. Condition reason and message updates are retained without resetting
  transition time unless the condition status changes.
- Sonarr skips HTTPRoute reconciliation when Gateway API is unavailable and
  continues reconciling its other resources.

### Storage and integration

- The Sonarr StatefulSet mounts the MediaLibrary's effective PVC directly when
  both resources are in the same namespace; Sonarr does not own its PVC.
- Cross-namespace media sharing remains unsupported until an RWX backend is
  selected. The existing MediaLibrary remains the sole owner of media data and
  its PVC lifecycle.
- HTTPRoute is enabled by default, but skipped if Gateway API is unavailable;
  other Sonarr resources continue reconciling.

### API, documentation, and product scope

- MediaLibrary PVC validation now requires a PVC name or managed PVC size and
  prevents mixing referenced-PVC and managed-PVC settings. A legacy zero size
  alongside a PVC name remains valid for typed Go clients.
- MediaLibrary printer columns now show the effective PVC and requested size.
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

### 1. Reconciliation reliability — complete

- [x] Safely handle omitted optional Sonarr security-context and MediaLibrary
  permission fields. Preserve the documented permission defaults where
  possible.
- [x] Add focused tests for omitted permission fields and default values.
- [x] Add focused tests for the remaining status and error paths.
- [x] Observe initialization Job changes and reconcile Sonarr when its
  referenced MediaLibrary changes.
- [x] Define retry behavior and conditions for resources waiting on dependencies:
  report unavailable status and return errors for controller-runtime's
  rate-limited retries; dependency watches trigger reconciliation when the
  dependency changes.
- [x] Make Gateway API availability non-blocking for Sonarr features that do
  not require HTTPRoute.

**RBAC approval:** Approved for manager `batch/jobs` permissions limited to
`create`, `get`, `list`, and `watch`, required by initialization and Job watches.

### 2. Storage design and completion — complete

- [x] Agree on ownership, namespace access, mounting, PV/PVC behavior, and
  cleanup semantics.
- [x] Replace the placeholder Sonarr media claim with the selected MediaLibrary
  PVC integration.
- [x] Reconcile PVs only if the approved storage model requires them, including
  the correct backing source.
- [x] Test the supported access modes and namespace cases.

The approved initial model supports same-namespace PVC mounting; no PV is
required. Sonarr does not own or delete the MediaLibrary PVC. Cross-namespace
sharing remains deferred pending selection of an RWX backend. B21–B23 passed in
the 23-spec local kind e2e run.

**Approval gate:** Obtain approval before further changes to storage behavior
or deletion policy.

### 3. Status and API correctness — complete

- [x] Ensure initialization and readiness status never report success for
  unusable dependencies.
- [x] Update existing conditions when their reason or message changes, while
  preserving transition-time semantics.
- [x] Correct MediaLibrary PVC validation and printer columns.
- [x] Report observed generation consistently where appropriate.

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
  test target must not depend on a live cluster. Planned e2e fixes and flows
  are in the [e2e test plan](e2e-plan.md).
