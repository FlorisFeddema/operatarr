# operatarr

**operatarr** is a Kubernetes operator designed to simplify the deployment and management of the Arr Stack of applications. The Arr Stack includes popular tools for automating and organizing media downloads, such as Radarr, Sonarr, Lidarr, and more.  
 
With operatarr, you can easily orchestrate the lifecycle of these applications in your Kubernetes clusters, ensuring automated updates, consistent configuration, seamless scaling, and robust monitoring. This operator streamlines complex operations, empowering you to run your media management stack reliably and efficiently in cloud-native environments.

## Features

TODO: List key features and functionalities.

## Installation

TODO: Provide installation instructions.

## Usage

TODO: Add examples for running and using operatarr.

## CI

Argo Workflows and Argo Events manifests for running this repository's CI from an external Argo installation live in [ci/argo/README.md](ci/argo/README.md).

GitHub Actions runs `make test` with race detection and coverage enabled. This
downloads the Makefile's pinned envtest binaries and runs unit and controller
tests against an isolated local API server; no existing cluster is required.
The cluster-dependent e2e suite is excluded and remains available through
`make test-e2e` against a configured kind cluster.

The current engineering intake and prioritized follow-up work are tracked in
[docs/roadmap.md](docs/roadmap.md).

## Contributing

Contributions are welcome! Please open issues or submit pull requests.

### Logging and errors

The operator and jobrunner use structured zap logging through `logr`. Both
executables expose the standard zap flags. Use `--zap-devel=false` for
production JSON output and `--zap-log-level=debug` when diagnosing routine
reconciliation steps. Existing development-mode defaults are unchanged.

Info logs describe startup, lifecycle transitions, and successful resource
changes; no-op reconciliation and dependency checks are debug logs. Returned
reconciliation failures are logged once by controller-runtime, which applies
its retry backoff. A deleted primary resource is not an error; missing
dependencies still surface as errors and through the existing status conditions.
Failed initialization Jobs retain their status-driven behavior and are logged
when their failure condition changes, rather than on every reconciliation.
The jobrunner logs initialization errors once and exits nonzero.

Implementation conventions for contributors and coding agents are in
[AGENTS.md](AGENTS.md#error-handling-and-logging).

### Development container

Open the repository in a Dev Container to use Go, common Go development tools,
kubectl, kind, and Podman. The Go feature installs its tools at their latest
versions when the container is built. Run `make build`, `make test`, or
`make lint` as needed. The Makefile downloads its versioned controller-gen,
kustomize, envtest, and lint tools on demand; the container stores them in
`~/.cache/operatarr/bin` to avoid reusing host binaries from the workspace's
`bin/` directory. In particular, `make lint` uses the Makefile's pinned linter,
not the one installed by the Go feature.

The container does not automatically mount Kubernetes credentials or a host
container-engine socket. To access a cluster, provide a kubeconfig and any
credentials it references, and ensure the cluster API is reachable from the
container. Image builds and kind-based e2e tests also need a working container
engine; nested Podman may require additional host and container configuration.
For kind with Podman, set `KIND_EXPERIMENTAL_PROVIDER=podman`. The e2e tests
operate on a configured kind cluster and install resources into it; they do not
create the cluster.

## License

This project is licensed under the Apache License 2.0. See the [LICENSE](LICENSE) file for details.

## Contact

Maintained by [Floris Feddema](https://github.com/FlorisFeddema).

---

*Please update this README with specific details about operatarr as your project evolves.*
