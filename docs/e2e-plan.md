# E2E test plan

This document records the work to make the cluster end-to-end (e2e) suite in
[test/e2e](../test/e2e/) reliable and faster, and the flows to add afterwards. The latest
recorded 25-spec run, including manager restart and upgrade flows B24 and B25,
passed on a local Linux/arm64 kind cluster. Items marked **approval** fall under the approval
boundaries in [AGENTS.md](../AGENTS.md) and need explicit sign-off before
implementation.

## Current state

The suite in [e2e_test.go](../test/e2e/e2e_test.go) has four ordered
Operator flows and the MediaLibrary flows in
[medialibrary_test.go](../test/e2e/medialibrary_test.go):

1. Setup verifies the current context is `kind-$(KIND_CLUSTER)`, builds and
   loads single-platform manager and jobrunner images, and applies the
   `test/e2e/overlay` Kustomize package.
2. B1 waits for the manager Deployment and pod to be healthy, verifies that
   both controllers start workers after cache synchronization, checks that the
   manager does not restart for ten seconds, and scans logs for RBAC errors.
3. B2 checks HTTPS `/metrics` access from an in-cluster curl pod using a
   ServiceAccount bound to the `metrics-reader` ClusterRole. The pod runs
   detached; the suite waits for it to succeed, reads its logs, and deletes it.
4. B3 verifies the leader-election Lease holder matches the manager pod.
5. B4 waits for both CRDs to report `Established=True`.
6. On test failure, the suite prints manager pods, descriptions, logs, and
   namespace events. Teardown removes the test-only metrics grant and overlay.

MediaLibrary flows B5–B13 are implemented. `make test` and CI exclude this
suite. The 20-spec suite (B1–B20) passed on Kubernetes v1.37.0 in a local
Linux/arm64 kind cluster with rootless Podman, using the single-node local-path
RWX provisioner. B10 verifies that increasing the MediaLibrary size updates
the PVC request; it does not assert backend capacity growth. The run took
about 5 minutes 52 seconds, and teardown removed the CRDs, operator, and test
storage. An earlier B8 run with NFS failed because the rootless node rejected
the NFS mount; the local-path backend replaced it.

The latest recorded full run passed all 25 specs on Kubernetes v1.37.0 in
318.922 seconds on Linux/arm64 kind with Podman 5.7.0. B24 suspends a
MediaLibrary initialization Job, restarts the manager, and verifies that the
same Job completes without duplicates. B25 creates a MediaLibrary with the
manager built from revision `cc648ee`, then upgrades to the current image and
verifies that the existing resource continues reconciling. This tests a
previous source revision, not a published release; compatibility with a
released image remains unverified. The earlier full run took about 7 minutes
5 seconds.

Sonarr flows B14–B20 are implemented in
[sonarr_test.go](../test/e2e/sonarr_test.go). They passed as part of the full
20-spec run, including HTTPRoute deletion and recreation. The suite installs
the pinned Gateway API HTTPRoute CRD and restarts the manager to test optional
API detection and route creation, then removes the CRD. An earlier run exposed
and fixed missing Gateway API scheme registration and Service drift detection.

Sonarr now mounts the referenced MediaLibrary PVC directly when both resources
are in the same namespace. B21–B23 cover Pod readiness and Service reachability,
same-namespace mounting without a PV, and the explicit unsupported condition
for cross-namespace references. The B21 test uses a local mock image that only
passes its `/ping` probe when `/media` is mounted. All three flows passed in
the latest full e2e run.

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
12. **CI (manual trigger selected, implemented).** A separate
    [e2e.yaml](../.github/workflows/e2e.yaml) workflow uses Docker and kind,
    sets `CONTAINER_TOOL=docker`, runs only through `workflow_dispatch`,
    always deletes the cluster, and uploads the test output and kind logs on
    failure.
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
but does not test multi-node storage or enforce capacity limits. The former
Ceph-specific `test/manifests/medialibrary.yaml` was unused and has been
removed; [config/samples](../config/samples/) holds the example resources.

| Flow | Assertions | Priority |
|------|------------|----------|
| B5 Existing ReadWriteMany PVC | `status.effectivePVC` is set, `Initialized=true`, `Available=True` with reason `ExistingPVCFound`, finalizer added, no managed PVC | 1 |
| B6 Existing ReadWriteOnce PVC rejected | `Available=False` with reason `ExistingPVCInvalidAccessMode`, not initialized | 1 |
| B7 Missing PVC created later | Initially `ExistingPVCNotFound`; recovers after the PVC is created | 1 |
| B8 Managed PVC and initialization Job | PVC created with owner reference and ReadWriteMany; `<name>-pvc-init` Job completes; reason `PVCInitialized`; `movies`, `tv`, and `music` folders exist (checked with a debug pod) | 1, after A3, A4, and ReadWriteMany storage |
| B9 Initialization Job failure | A failing Job leads to reason `PVCInitializationFailed` and not initialized | 1 |
| B10 Resize | Increasing the size on an expandable StorageClass updates the PVC request | 2, implemented and passed |
| B11 StorageClass change rejected | Error or condition reported, PVC unchanged | 1 |
| B12 Deletion | With `pvcRetentionPolicy: Delete`, deleting the MediaLibrary removes its managed PVC; with `Retain`, the PVC and data remain while the initialization Job is cleaned up. A PVC supplied by `spec.pvc.pvcName` is never deleted. | 1, implemented |
| B13 Idempotency | Resource versions of the MediaLibrary, PVC, and Job stay stable across resyncs | 1 |

### Sonarr

Sonarr mounts the referenced MediaLibrary PVC directly when it is in the same
namespace. Cross-namespace references report
`CrossNamespaceMediaLibraryUnsupported`, regardless of
`crossNamespaceAccess`, until a shareable RWX backend is selected. Disabling
routing deletes the Sonarr-owned HTTPRoute, even if its MediaLibrary is
unavailable; a route disabled from creation is not created.

| Flow | Assertions | Priority |
|------|------------|----------|
| B14 Missing MediaLibrary | `Available=False` with reason `MediaLibraryUnavailable`; recovers after the MediaLibrary is created | 1 |
| B15 Resources created without Gateway API | With `crossNamespaceAccess: true`, the StatefulSet, Service, config PVC template, and owner references exist; no HTTPRoute; `Available=True` | 1 |
| B16 Gateway API installed | After installing the pinned HTTPRoute CRD and restarting the manager, an HTTPRoute is created with the configured hostname and parentRef; disabling deletes it, and re-enabling recreates it | 1 |
| B17 HTTPRoute validation | An enabled route without a hostname is rejected by the API server | 1 |
| B18 Spec updates | Changing the image or resource requests updates the StatefulSet | 1 |
| B19 Drift correction | A manually edited or deleted Service is restored | 1 |
| B20 Deletion | Owned resources are garbage-collected and the finalizer is removed | 1 |
| B21 Pod ready and reachable | Pod runs with the media volume mounted and returns HTTP 200 through the Service | 2, implemented and passed |
| B22 `crossNamespaceAccess: false` | Same-namespace PVC mounts directly; no PV is created | 2, implemented and passed |
| B23 MediaLibrary in another namespace | Sonarr reports `CrossNamespaceMediaLibraryUnsupported` and creates no dependent resources | 2, implemented and passed |

### Robustness

| Flow | Assertions | Priority |
|------|------------|----------|
| B24 Manager restart during reconciliation | State converges with no duplicate Jobs | 2, implemented and passed |
| B25 Upgrade from the previous release | Existing resources keep working after upgrading the manager image | 2, passed with previous source revision; release image not yet tested |

## Suggested order

1. Harness fixes A2, A3, and A5–A11, including the approved minimal RBAC A1;
   then B1–B4. **Implemented and passed on Linux/arm64 kind.** The AGENTS.md
   e2e rule (step 14) is also in place.
2. Single-node RWX provisioner; then B5–B13. **Implemented and passed on
   Linux/arm64 kind**, including the A4 and managed-PVC reconciliation fixes.
3. Sonarr flows B14–B20. **Implemented and passed on Linux/arm64 kind.**
4. `pvcRetentionPolicy` supports `Delete` (the default) and `Retain` for
   operator-managed PVCs; referenced user PVCs are untouched. **Implemented
   and passed on Linux/arm64 kind**; B12 verifies deletion, retention, retained
   data, and initialization Job cleanup.
5. Add the manually triggered CI job (step 12) once the suite is stable and
   runs in roughly ten minutes. **Implemented** as a separate
   [e2e.yaml](../.github/workflows/e2e.yaml) workflow; not yet run in CI.
6. Sonarr storage flows B21–B23. **Implemented and passed on Linux/arm64 kind.**
   Cross-namespace data sharing remains deferred until an RWX backend is chosen.
7. Manager restart during MediaLibrary initialization (B24). **Implemented and
   passed on Linux/arm64 kind.**
8. Manager upgrade from the previous release (B25). **Passed with a locally
   built previous source revision; a published release image remains to be tested.**

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
  default `Delete`. This preserves existing behavior for resources that omit
  the field. The setting applies to operator-managed PVCs; referenced existing
  PVCs remain user-owned and untouched. `Retain` removes the MediaLibrary owner
  reference before the finalizer completes. Schema and cleanup behavior
  approved and implemented.
- **Cluster:** use a local kind cluster. This is a target choice, not approval
  to mutate a cluster; obtain explicit confirmation before creating it. A
  working container engine and kind setup are also still prerequisites.
- **CI trigger:** manual (`workflow_dispatch`) only; do not run e2e on every PR
  or automatically by path.

## Part C: make local E2E faster

The pre-optimization comparison run took about 7 minutes 5 seconds for all 25
specs on Linux/arm64 kind. On 2026-11-21, the latest updated-suite run passed
all 25 specs in 318.922 seconds with B25 enabled using the local `cc648ee`
manager image. This is about 1 minute 46 seconds (25%) lower than the
historical run. The prior updated-suite run took 313.195 seconds; treat the
difference as normal run variation, not a regression. These results are
indicative, not a controlled benchmark; machine and image-cache state affect
timing. A run without the previous image completed 24 of 25 specs in 292.290
seconds, with B25 skipped.

### Phase 1: reduce repeated work

1. **Reuse Go build caches. Implemented.** The three E2E Dockerfiles now use
   BuildKit cache mounts for Go compilation (and module downloads where
   applicable), remove `go build -a`, and use `-trimpath`. Dockerfile source
   inputs are already copied selectively, so broadening `.dockerignore` is not
   part of this change.
2. **Replace the fixed cache-sync delay. Implemented.** B1 waits for both
   controller-runtime workers to start, then checks pod restarts for ten
   seconds and scans manager logs. The full suite also scans logs after all
   specs, catching RBAC errors introduced by later reconciliations. Reaching
   Ready conditions in the resource flows exercises the relevant write paths.
3. **Run labeled subsets. Implemented.** `E2E_LABELS` passes a Ginkgo
   label-filter; available labels are `operator`, `medialibrary`, `sonarr`,
   `upgrade`, `restart`, and `slow`. MediaLibrary test storage setup is shared
   with Sonarr so those subsets remain runnable independently.
4. **Make the upgrade image optional. Implemented.** Without
   `E2E_PREVIOUS_MANAGER_IMAGE`, B25 is skipped and image preparation avoids
   pulling, saving, or loading a previous-release image. CI continues to
   require its configured release image.

### Phase 2: improve repeat local runs

5. **Add timing/report output. Implemented.** E2E output includes durations for
   image build/save, image load, overlay deployment, and the full suite through
   teardown. Ginkgo writes per-spec JSON to `$(LOCALBIN)/e2e-report.json` by
   default; `E2E_REPORT` can override the path. Use the report to identify
   remaining hotspots before changing the harness further. The latest run
   measured image build/save at 15.744 seconds, kind image load at 1.920
   seconds, overlay apply at 0.279 seconds, and the suite including teardown at
   318.921 seconds. The first and last Ginkgo spec timings include shared
   `BeforeAll` and `AfterAll` work: B1's 39.39 seconds includes image setup and
   deployment, while B20's 67.96 seconds includes suite cleanup. B8's isolated
   spec duration was 43.11 seconds; B25 took 34.69 seconds.
   Teardown now reports cleanup timing for manager-log checks, individual
   MediaLibrary resource and storage-removal steps, metrics access, the
   operator overlay, and the optional Gateway API CRD. PV cleanup logs each
   poll's remaining volume names, phases, finalizers, and claims; namespace
   cleanup logs its phase, deletion conditions, and remaining namespaced
   resources while waiting up to two minutes for deletion to finish.
   The first measured full run passed 24 specs and skipped B25 in 251.559
   seconds. MediaLibrary cleanup took 57.734 seconds; deleting its idle
   volume-protection pods took 30.635 seconds, while waiting for Persistent
   Volumes and deleting the test namespace took 10.035 and 10.446 seconds.
   The protection pods now use `exec sleep` and a one-second termination grace
   to avoid the default 30-second pod shutdown wait. A focused MediaLibrary
   run passed 10 specs (15 skipped); the same pod-deletion step took 3.041
   seconds, about 27.6 seconds less. Since the runs selected different specs,
   compare this cleanup step rather than their total durations. PV deletion
   and namespace deletion remain about 10 seconds each.
   Detailed polling showed PV counts of 8, 8, 5, 4, 2, then 0 over 10.056
   seconds; remaining volumes were `Released` with the PV-protection
   finalizer. Namespace deletion took 11.086 seconds. At about 5.1 seconds,
   namespace conditions already reported content removed and no finalizers,
   while the provisioner pod, Deployment/ReplicaSet, and support objects were
   still disappearing. The configured poll intervals account for at most
   about two seconds of PV wait and one second of namespace wait, so the
   remaining delay appears to be provisioner/control-plane cleanup; no storage
   policy or deletion wait was relaxed.
6. **Reduce slow waits in E2E helpers. B8 updated and focused E2E passed.**
   Its concurrent RWX check keeps writer containers alive with `exec sleep` so
   Kubernetes can signal the sleeping process directly during normal pod
   deletion, with a one-second termination grace after file assertions
   complete. This preserves the cross-pod read/write assertions. A focused B8
   run passed in 88.795 seconds, compared with 116.830 seconds before the
   shorter grace; both totals include shared setup and teardown. The RWX-check
   step itself fell from about 32 seconds to about 5 seconds in the run logs.
7. Add an opt-in reuse mode that retains the operator, CRDs, and test storage
   across runs. Keep cleanup explicit with a separate full-teardown target;
   verify it cannot leave test resources behind.
8. Move API-only assertions to envtest where they exercise the same behavior.
   Keep cluster E2E for pods, Jobs, storage, garbage collection, RBAC, and
   manager restarts.

### Phase 3: parallelize independent flows

9. Split independent flows into separate Ginkgo groups/namespaces and enable
   parallel execution. Keep tests that restart or upgrade the shared manager
   serial, and measure contention before making parallelism the default.

### Phase 4: CI image caching

10. Add container build-cache configuration to the manual E2E workflow and
   avoid pulling an unchanged previous-release image when a reliable cache key
   is available. Preserve the manual-only trigger decision.
