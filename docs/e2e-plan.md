# E2E test plan

This document records the work to make the cluster end-to-end (e2e) suite in
[test/e2e](../test/e2e/) reliable and the flows to add afterwards. The harness
and Operator flows B1–B4 passed on a local Linux/arm64 kind cluster. Items
marked **approval** fall under the approval boundaries in
[AGENTS.md](../AGENTS.md) and need explicit sign-off before implementation.

## Current state

The suite in [e2e_test.go](../test/e2e/e2e_test.go) has four ordered
Operator flows and the MediaLibrary flows in
[medialibrary_test.go](../test/e2e/medialibrary_test.go):

1. Setup verifies the current context is `kind-$(KIND_CLUSTER)`, builds and
   loads single-platform manager and jobrunner images, and applies the
   `test/e2e/overlay` Kustomize package.
2. B1 waits for the manager Deployment and pod to be healthy, checks that the
   manager does not restart for two minutes, and scans logs for RBAC errors.
3. B2 checks HTTPS `/metrics` access from an in-cluster curl pod using a
   ServiceAccount bound to the `metrics-reader` ClusterRole.
4. B3 verifies the leader-election Lease holder matches the manager pod.
5. B4 waits for both CRDs to report `Established=True`.
6. On test failure, the suite prints manager pods, descriptions, logs, and
   namespace events. Teardown removes the test-only metrics grant and overlay.

MediaLibrary flows B5–B9, B11, and B13 are implemented. `make test` and CI
exclude this suite. B1–B7 passed on Kubernetes v1.37.0 in the local Linux/arm64
kind cluster. The first B8 run exposed a managed-PVC reconciliation bug;
after the fix, its PVC bound and its Job scheduled, but the rootless node
rejected the NFS mount. The suite cleaned up its resources and left the
cluster running. A single-node local-path RWX backend is now being validated.

## Part A: make the existing suite work

### Original blockers

| # | Blocker | Evidence | Gate |
|---|---------|----------|------|
| A1 | The manager ClusterRole lacks permissions for core `persistentvolumeclaims`, `persistentvolumes`, and `services`, for `apps/statefulsets`, and for `gateway.networking.k8s.io/httproutes`. The controllers `Owns()` these types, so their informers cannot sync and the manager is expected to exit after the cache-sync timeout. The smoke test can still pass because it only checks `phase=Running` within one minute. Envtest-based tests run with admin credentials and do not catch this. | [role.yaml](../config/rbac/role.yaml), [Sonarr watches](../internal/controller/sonarr_controller.go#L76), [MediaLibrary watches](../internal/controller/medialibrary_controller.go#L72) | **approved** (minimal required RBAC) |
| A2 | `make docker-build` runs `$(CONTAINER_TOOL) buildx build --manifest` for `linux/arm64,linux/amd64`. This produces a Podman manifest list; `kind load docker-image` expects a single image in the local Docker store. | [Makefile](../Makefile#L112) | — |
| A3 | The jobrunner image is never built or loaded. The manager defaults to `harbor.feddema.dev/operatarr/job-runner:dev` with `IfNotPresent`, so initialization Jobs cannot start in kind. | [main.go](../cmd/main.go#L59), [init Job](../internal/controller/medialibrary_controller.go#L338) | — |
| A4 | The initialization Job hardcoded `nodeSelector: kubernetes.io/arch=arm64`, so it could not schedule on amd64 nodes. The selector has been removed and e2e images target the kind nodes' architecture. Image building, loading, and Job scheduling passed on arm64. | [init Job](../internal/controller/medialibrary_controller.go), [image preparation](../test/e2e/e2e_test.go) | **approved** and implemented |
| A5 | Prometheus Operator and cert-manager are installed even though neither is enabled in the default overlay. `kubectl create` for Prometheus fails when rerun, and `AfterAll` removes add-ons that may have been preinstalled. | [e2e_test.go](../test/e2e/e2e_test.go#L33), [default overlay](../config/default/kustomization.yaml#L23) | — |
| A6 | There is no guard on the target cluster: the suite uses whatever kubeconfig context is current and deletes `operatarr-system`. | [e2e_test.go](../test/e2e/e2e_test.go#L45) | — |
| A7 | `make deploy` runs `kustomize edit set image`, rewriting the tracked [manager kustomization](../config/manager/kustomization.yaml), and also regenerates manifests. | [Makefile](../Makefile#L150) | — |
| A8 | Teardown does not undeploy the manager or uninstall the CRDs. Custom resources with finalizers can block namespace deletion once the controller is gone. | [e2e_test.go](../test/e2e/e2e_test.go#L45) | — |
| A9 | Failures produce no diagnostics such as manager logs, events, or resource descriptions. | — | — |
| A10 | Nested rootless Podman may be unable to create user namespaces (`newuidmap ... Operation not permitted`). A local kind cluster needs a working host container-engine connection and a reachable API endpoint. | [development container](../README.md#development-container) | local setup; ask before cluster mutations |
| A11 | The default RBAC Kustomization referenced missing `library_editor_role.yaml` and `library_viewer_role.yaml` files, preventing both default and e2e overlays from rendering. | [RBAC kustomization](../config/rbac/kustomization.yaml) | fixed by removing dangling references |

### Steps

1. **Cluster lifecycle.** Add `KIND_CLUSTER ?= operatarr-e2e` and
   `kind-create`/`kind-delete` Makefile targets, with
   `hack/kind-config.yaml` for the single-node cluster. The e2e target should use the
   `kind-$(KIND_CLUSTER)` context explicitly.
2. **Cluster guard.** In `BeforeAll`, fail unless the current context is
   exactly `kind-$KIND_CLUSTER`.
3. **Images.** Add single-architecture local build targets for the manager and
   jobrunner (`$(CONTAINER_TOOL) build --platform linux/$(GOARCH) -t ...`).
   Save Docker-compatible archives and load them with `kind load image-archive`
   for Docker or Podman.
4. **Deploy without modifying tracked files.** Add a test overlay, for example
   `test/e2e/overlay/`, based on `config/default`. It sets the manager image and
   adds `--job-runner-image=<e2e tag>`. Apply it with `kubectl apply -k`
   instead of `make deploy`.
5. **Remove unused add-ons.** Drop the Prometheus Operator and cert-manager
   setup until ServiceMonitor or webhooks are enabled.
6. **RBAC (approved).** Add `+kubebuilder:rbac` markers for the resources the
   controllers own or create, run `make manifests`, and review the generated
   diff. Verify with
   `kubectl auth can-i --as=system:serviceaccount:operatarr-system:operatarr-controller-manager`.
7. **Init Job architecture (implemented).** Remove the arm64 `nodeSelector` and
   build/load the jobrunner image for the kind node architecture. Image
   building, loading, and Job scheduling passed on arm64.
8. **Stronger smoke test.** Wait for the Deployment to be `Available` and the
   pod `Ready`. Then check that `restartCount` stays 0 for longer than the
   cache-sync timeout and that the logs contain no `Forbidden` or
   `cannot list resource` errors.
9. **Diagnostics.** On failure, collect manager logs, `kubectl get events -A`,
   and descriptions of the test resources and pods.
10. **Teardown.** Delete test custom resources and wait until their finalizers
    are removed. Then undeploy the manager, uninstall the CRDs, and delete the
    namespaces, using `--ignore-not-found` throughout so the suite can be rerun.
11. **Helpers.** Add reusable helpers to [test/utils](../test/utils/), such as
    applying YAML, waiting for conditions, and reading JSONPath values, while
    preserving the current `Run` behavior.
12. **CI (manual trigger selected).** Add an e2e job to
    [go.yaml](../.github/workflows/go.yaml) that uses Docker and kind, sets
    `CONTAINER_TOOL=docker`, runs only through `workflow_dispatch`, and uploads
    diagnostics on failure.
13. **Documentation.** Document prerequisites, environment variables, and the
    `make kind-create test-e2e kind-delete` workflow in the README.
14. **Agent rule (after the harness and B1–B4 are implemented).** Add a rule to
    [AGENTS.md](../AGENTS.md) asking agents to suggest new or updated e2e
    flows when a change affects cluster-level behavior, such as new resources,
    RBAC, watches, storage, or deletion. Agents should propose the flows rather
    than run the suite, because e2e still requires cluster approval.

## Part B: flows to add

Priority 1 flows work with the current controller code once Part A is done.
Priority 2 flows depend on the storage design in
[roadmap phase 2](roadmap.md#2-storage-design-and-completion) or on other
product fixes.

### Operator

| Flow | Assertions | Priority |
|------|------------|----------|
| B1 Manager healthy | Deployment `Available`, pod `Ready`, no restarts over the cache-sync window, no RBAC errors in logs | 1 |
| B2 Metrics endpoint | `/metrics` on port 8443 returns 200 from a curl pod using a token bound to the metrics-reader role | 1 |
| B3 Leader election | Lease `f9ce3012.feddema.dev` is held by the manager pod | 1 |
| B4 CRDs established | Both CRDs report `Established=True` | 1 |

### MediaLibrary

Kind's default `standard` (local-path) StorageClass supports only
ReadWriteOnce, while managed MediaLibrary PVCs request ReadWriteMany. NFS
provisioning worked, but NFS mounts failed with `Operation not permitted` on
the rootless kind node. The approved test setup uses a separate local-path
provisioner configured with a shared filesystem path, isolated StorageClass
and RBAC. It supports concurrent access from pods on the single kind node,
but does not test multi-node storage or enforce capacity limits. The existing
[test/manifests/medialibrary.yaml](../test/manifests/medialibrary.yaml) uses a
Ceph-specific StorageClass and should be replaced or removed.

| Flow | Assertions | Priority |
|------|------------|----------|
| B5 Existing ReadWriteMany PVC | `status.effectivePVC` is set, `Initialized=true`, `Available=True` with reason `ExistingPVCFound`, finalizer added, no managed PVC | 1 |
| B6 Existing ReadWriteOnce PVC rejected | `Available=False` with reason `ExistingPVCInvalidAccessMode`, not initialized | 1 |
| B7 Missing PVC created later | Initially `ExistingPVCNotFound`; recovers after the PVC is created | 1 |
| B8 Managed PVC and initialization Job | PVC created with owner reference and ReadWriteMany; `<name>-pvc-init` Job completes; reason `PVCInitialized`; `movies`, `tv`, and `music` folders exist (checked with a debug pod) | 1, after A3, A4, and ReadWriteMany storage |
| B9 Initialization Job failure | A failing Job leads to reason `PVCInitializationFailed` and not initialized | 1 |
| B10 Resize | Increasing the size on an expandable StorageClass updates the PVC request | 2, needs an expandable ReadWriteMany class |
| B11 StorageClass change rejected | Error or condition reported, PVC unchanged | 1 |
| B12 Deletion | With `pvcRetentionPolicy: Delete`, deleting the MediaLibrary removes its managed PVC; with `Retain`, the PVC and data remain while the initialization Job is cleaned up. A PVC supplied by `spec.pvc.pvcName` is never deleted. | 1, requires the approved API field and cleanup behavior |
| B13 Idempotency | Resource versions of the MediaLibrary, PVC, and Job stay stable across resyncs | 1 |

### Sonarr

Current limitations: the StatefulSet mounts the placeholder claim
[`mediaPvcName`](../internal/controller/sonarr_controller.go#L388), so the
Sonarr pod stays `Pending`. When `crossNamespaceAccess` is `false`,
[`reconcileMediaPersistentVolume`](../internal/controller/sonarr_controller.go#L542)
creates a PV without a volume source, which a real API server rejects.

| Flow | Assertions | Priority |
|------|------------|----------|
| B14 Missing MediaLibrary | `Available=False` with reason `MediaLibraryUnavailable`; recovers after the MediaLibrary is created | 1 |
| B15 Resources created without Gateway API | With `crossNamespaceAccess: true`, the StatefulSet, Service, config PVC template, and owner references exist; no HTTPRoute; `Available=True` | 1 |
| B16 Gateway API installed | After installing the Gateway API CRDs and restarting the manager, an HTTPRoute is created with the configured hostname and parentRef; disabling the route removes or skips it | 1 |
| B17 HTTPRoute validation | An enabled route without a hostname is rejected by the API server | 1 |
| B18 Spec updates | Changing the image or environment updates the StatefulSet | 1 |
| B19 Drift correction | A manually edited or deleted Service is restored | 1 |
| B20 Deletion | Owned resources are garbage-collected and the finalizer is removed | 1 |
| B21 Pod ready and reachable | Pod runs with the media volume mounted and returns HTTP 200 through the Service | 2 |
| B22 `crossNamespaceAccess: false` | PV creation path works | 2 |
| B23 MediaLibrary in another namespace | Cross-namespace reference behaves as designed | 2 |

### Robustness (later)

| Flow | Assertions | Priority |
|------|------------|----------|
| B24 Manager restart during reconciliation | State converges with no duplicate Jobs | 2 |
| B25 Upgrade from the previous release | Existing resources keep working after upgrading the manager image | 2 |

## Suggested order

1. Harness fixes A2, A3, and A5–A11, including the approved minimal RBAC A1;
   then B1–B4. **Implemented and passed on Linux/arm64 kind.** The AGENTS.md
   e2e rule (step 14) is also in place.
2. Single-node RWX provisioner; then B5–B9 and B11–B13. A4 and managed-PVC
   reconciliation fixes are implemented; live initialization needs validation.
3. Sonarr flows B14–B20.
4. Implement the approved `pvcRetentionPolicy` API and behavior, then verify
   both deletion choices in B12.
5. Add the manually triggered CI job (step 12) once the suite is stable and
   runs in roughly ten minutes.
6. Priority 2 flows after the storage design is approved.

## Decisions recorded

- **RBAC:** approved; add only permissions needed by the current controller
  watches and reconciliation (A1).
- **Init Job architecture:** approved; remove the hardcoded arm64 selector and
  target the kind node architecture (A4).
- **ReadWriteMany storage:** use an isolated shared-path local provisioner on
  the single-node kind cluster, and verify concurrent pod access. This is not
  a multi-node storage test.
- **MediaLibrary PVC retention:** support both deletion behaviors with the
  proposed schema `spec.pvcRetentionPolicy`, enum values `Delete` and `Retain`,
  default `Delete`. This would preserve existing behavior for resources that
  omit the field. The setting would apply to operator-managed PVCs; referenced
  existing PVCs would remain user-owned and untouched. `Retain` would require
  removing the MediaLibrary owner reference before the finalizer completes.
  Obtain explicit approval for the schema and cleanup behavior before editing.
- **Cluster:** use a local kind cluster. This is a target choice, not approval
  to mutate a cluster; obtain explicit confirmation before creating it. A
  working container engine and kind setup are also still prerequisites.
- **CI trigger:** manual (`workflow_dispatch`) only; do not run e2e on every PR
  or automatically by path.
