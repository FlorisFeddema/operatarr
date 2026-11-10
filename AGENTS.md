# Repository instructions

## Project and navigation

Operatarr is a Go Kubernetes operator for the ARR/media stack, targeting apps
such as Sonarr, Radarr, Jellyfin, and Seerr. Currently, the repository implements
Sonarr and MediaLibrary resources; do not assume the other apps already exist.

- [API types](api/v1alpha1/): custom resource definitions and deepcopy sources.
- [Controllers](internal/controller/): reconciliation logic and controller tests.
- [Controller helpers](internal/controller/helpers.go) and
  [shared utilities](internal/utils/core.go): existing reusable logic.
- [Manager](cmd/main.go): controller registration and capability detection.
- [Jobrunner](jobrunner/): separate program for MediaLibrary initialization.
- [Configuration](config/): manifests, RBAC, samples, and deployment overlays.
- [Makefile](Makefile): authoritative build, test, and code-generation commands.
- [README](README.md): development environment and cluster prerequisites.

## Human approval boundaries

- Obtain explicit human confirmation before changing any CRD schema. This
  includes adding CRDs, changing spec or status fields, JSON names, types,
  requiredness, validation, defaults, API versions, or resource scope. Changes to
  API source types or Kubebuilder markers count even before manifest generation.
  Describe the proposed change and its compatibility impact before editing;
  approval applies only to that scope.
- Ordinary runtime status updates that use the existing schema do not require
  schema approval.
- Ask before expanding RBAC permissions or changing storage behavior,
  finalizer cleanup, or deletion policy.
- Ask before any cluster mutation, including creating a local test cluster,
  installing CRDs, deploying/uninstalling the operator, applying/deleting
  resources, or running cluster-mutating e2e tests. Also ask before pushing images.
  Do not assume the current kubeconfig points to a disposable cluster.

## Reconciliation architecture

- Keep resource controllers structurally consistent: load desired state,
  handle finalizers/deletion, then reconcile resource-specific desired state.
  Follow [Sonarr](internal/controller/sonarr_controller.go) and
  [MediaLibrary](internal/controller/medialibrary_controller.go) as existing
  examples, preserving their resource-specific behavior.
- Reuse existing helpers and shared reconciliation steps. When related code is
  touched, extract genuinely shared behavior rather than copying it into another
  controller. Prefer small, explicit steps over a speculative generic framework;
  do not refactor unrelated controllers just to unify them.
- Preserve step dependencies and ordering. Use concurrent execution only for
  independent steps; a common lifecycle does not mean identical steps or
  concurrency for every resource.
- Make reconciliation idempotent and safe to retry. Preserve ownership,
  finalizers, conditions, error propagation, and requeue behavior. Ensure watches
  cover relevant changes to dependent resources.
- Keep per-reconciliation state local to the request. Do not introduce mutable
  shared request state into the reconciler.
- Preserve optional Gateway API behavior: an unavailable Gateway API must not
  make otherwise supported resources depend on HTTPRoute creation.

## Implementation and generated files

- Make focused changes using existing Go, controller-runtime, and test patterns.
  Avoid unrelated refactors, new dependencies without a clear need, and
  abstractions that do not reduce real duplication.
- Treat [deepcopy code](api/v1alpha1/zz_generated.deepcopy.go),
  [CRD bases](config/crd/bases/), and generated controller RBAC as generated
  outputs: change their source and regenerate, rather than editing them by hand.
- `make generate` regenerates deepcopy code; `make manifests` regenerates CRDs
  and RBAC. Regeneration is not permission to change schemas or expand RBAC.
  If generation produces unexpected schema or permission changes, stop and ask.
- If a required tool is missing or needs to be downloaded, ask the user whether
  it may be added to the devcontainer before downloading it or changing its
  configuration.
- Update directly affected documentation and samples when behavior changes.
  Preserve existing user changes and never commit credentials.

## Validation

- For Go changes, format touched code, run relevant focused tests, then run
  `make vet` and `go build ./...`. Add or update tests for changed behavior,
  including idempotency and error/deletion paths where relevant.
- For changes spanning controllers or approved API changes, also run `make test`.
  Consult the [Makefile](Makefile) and
  [controller test suite](internal/controller/suite_test.go) when selecting
  focused tests and configuring envtest assets.
- `make test` and `make build` also regenerate artifacts. Do not treat them as
  read-only checks; inspect unexpected generated changes against the approval
  boundaries above.
- Use the Go version declared in [go.mod](go.mod). Respect the environment's
  `LOCALBIN` override for tool downloads; do not assume bundled binaries match
  the current OS/architecture.
- E2E requires a configured cluster and working container engine; follow the
  [README](README.md) and obtain approval before running it. Do not substitute a
  live cluster for missing envtest assets.
- Documentation-only changes do not require Go tests. Report what was validated
  and any blocked checks accurately; never claim an unrun check passed.
