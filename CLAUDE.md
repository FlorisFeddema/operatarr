# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is Operatarr

A Kubernetes operator (built with Kubebuilder v4) that automates deployment and management of the Arr Stack (Sonarr, Radarr, Lidarr, etc.) in Kubernetes clusters. It manages two custom resources: `Sonarr` and `MediaLibrary`.

## Development Commands

```bash
make build          # Build the manager binary
make run            # Run controller locally against current kubeconfig
make test           # Run unit/integration tests with envtest
make test-e2e       # Run e2e tests against a Kind cluster
make lint           # Run golangci-lint
make lint-fix       # Run golangci-lint with auto-fix
make fmt            # Run go fmt
make manifests      # Regenerate CRDs, RBAC, webhook manifests
make generate       # Regenerate DeepCopy methods after API changes
make install        # Install CRDs into the current cluster
make deploy         # Deploy the controller to the current cluster
```

Run a single test suite:
```bash
go test ./internal/controller/... -v -run "TestControllers"
```

## Architecture

### Entry Point: `cmd/main.go`
Initializes the controller-runtime manager, optionally detects Gateway API availability (for HTTPRoute support), and registers both reconcilers. Key flags: `--job-runner-image`, `--timezone`.

### Custom Resources: `api/v1alpha1/`
- **Sonarr** – Defines a Sonarr instance. Requires `image`, `configVolumeSpec`, and a `mediaLibraryRef`. Optionally configures `hostname` and `parentRef` for Gateway API HTTPRoute creation.
- **MediaLibrary** – Manages shared media PVCs. Supports creating a new PVC or adopting an existing one. Tracks initialization state in `.status`.

### Reconcilers: `internal/controller/`
Each reconciler follows the pattern: `Reconcile() → loadDesiredState() → preconcile() → reconcile()`. The `reconcile()` step calls sub-functions for each owned resource (Service, StatefulSet, PVC, HTTPRoute).

### Job Runner: `jobrunner/`
A separate binary (`-mode medialibrary`) that runs as a Kubernetes Job to initialize MediaLibrary directories and file permissions. Built and pushed as a separate Docker image, referenced by `--job-runner-image`.

### Utilities: `internal/utils/`
Concurrent helper (`RunConcurrently`), condition management, and resource parsing utilities shared across reconcilers.

## Key Conventions

- After any change to `api/v1alpha1/` types, run `make generate && make manifests` to update generated code and CRD YAMLs.
- The controller optionally creates Gateway API `HTTPRoute` resources only when the Gateway API CRDs are detected at startup; the `gatewayAvailable` flag gates this.
- Tests use Ginkgo/Gomega with `envtest`; e2e tests require a running Kind cluster.
- CI runs via Argo Workflows (see `ci/argo/README.md`); GitHub Actions (`.github/workflows/go.yaml`) handles lint, build, and test on PRs.
