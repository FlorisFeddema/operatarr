# operatarr

**operatarr** is a Kubernetes operator designed to simplify the deployment and management of the Arr Stack of applications. The Arr Stack includes popular tools for automating and organizing media downloads, such as Radarr, Sonarr, Lidarr, and more.  
 
With operatarr, you can easily orchestrate the lifecycle of these applications in your Kubernetes clusters, ensuring automated updates, consistent configuration, seamless scaling, and robust monitoring. This operator streamlines complex operations, empowering you to run your media management stack reliably and efficiently in cloud-native environments.

## Features

TODO: List key features and functionalities.

## Installation

TODO: Provide installation instructions.

## Usage

TODO: Add examples for running and using operatarr.

For Sonarr resources, disabling `spec.sonarrHttpRouteSpec.enabled` deletes the
Sonarr-owned HTTPRoute. Re-enabling it recreates the route when Gateway API is
available and the referenced MediaLibrary is initialized.

## CI

Argo Workflows and Argo Events manifests for running this repository's CI from an external Argo installation live in [ci/argo/README.md](ci/argo/README.md).

GitHub Actions runs `make test` with race detection and coverage enabled. This
downloads the Makefile's pinned envtest binaries and runs unit and controller
tests against an isolated local API server; no existing cluster is required.
The cluster-dependent e2e suite is excluded and remains available through
`make test-e2e` against a disposable kind cluster. The suite expects the
`kind-operatarr-e2e` context by default, builds and loads local manager and
jobrunner images, installs the CRDs and operator, and removes those resources
afterward. It also checks the manager health, metrics authorization,
leader-election lease, and CRD establishment.
The Sonarr flows require the Gateway API HTTPRoute CRD to be absent initially.
They temporarily install the CRD from the pinned Go module and restart the
manager to verify Gateway API discovery, then remove the CRD during cleanup.

With `kind`, `kubectl`, and a working Docker or Podman installation available,
run `make kind-create`, `make test-e2e`, and `make kind-delete`. The cluster
uses the single-node configuration in [hack/kind-config.yaml](hack/kind-config.yaml).
To keep its credentials separate, set `KUBECONFIG` to a separate file before
running these commands. The Makefile defaults to Podman; use
`CONTAINER_TOOL=docker` for Docker. For Podman, kind may require
`KIND_EXPERIMENTAL_PROVIDER=podman`. Set `KIND_CLUSTER` to use a different
cluster name. The suite refuses to run unless the current kubeconfig context
matches `kind-$(KIND_CLUSTER)` and explicitly targets that context for each
cluster command. The manager and jobrunner images are built for the kind
nodes' architecture; all nodes must have the same Linux architecture. The
MediaLibrary tests require a single-node kind cluster and its default
local-path provisioner. They temporarily install a separate provisioner,
StorageClass, and RBAC in a uniquely named namespace, reusing the kind
provisioner and helper images. Its test volumes use isolated directories under
`/tmp` inside the kind node. Two pods exercise concurrent read/write access;
this single-node setup does not validate multi-node sharing or enforce
requested storage capacity. Test PVCs, volumes, and directories are removed
before the provisioner is removed. If cleanup fails, the suite leaves the
operator and test storage in place for recovery.
The suite does not create or delete the cluster; those actions are explicit
Makefile targets. Use only a disposable cluster because the suite applies and
removes cluster-scoped CRDs and RBAC.

The [E2E workflow](.github/workflows/e2e.yaml) runs this suite in GitHub
Actions only when triggered manually (`workflow_dispatch`). It uses Docker and
a kind cluster on the runner, which it always deletes afterwards, and uploads
the test output and exported kind logs as the `e2e-diagnostics` artifact when
a run fails.

MediaLibrary initialization Jobs are not pinned to a specific architecture.
The configured jobrunner image must support every architecture on which those
Jobs may be scheduled. For managed PVCs, the operator preserves
Kubernetes-assigned claim fields and starts the initialization Job after
reconciling the claim; the Job can wait for the PVC to bind.

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
kubectl, kind, Podman, and ripgrep. The Go feature installs its tools at their
latest versions when the container is built. On creation, the container runs
`make devcontainer-setup` to download Go modules, the Makefile-pinned
controller-gen, setup-envtest and linter, and the matching Linux envtest
assets. This requires network access and may take some time. Run the same
command manually to retry a failed setup. Run `make build`, `make test`, or
`make lint` as needed. The Makefile downloads other versioned tools on demand;
the container stores them in `~/.cache/operatarr/bin` to avoid reusing host
binaries from the workspace's `bin/` directory. In particular, `make lint`
uses the Makefile's pinned linter, not the one installed by the Go feature.

Named volume mounts for Copilot sessions (`~/.copilot`) and VS Code user data
(`~/.vscode-server/data/User`, including chat workspace storage) are enabled by
default in `.devcontainer/devcontainer.json`. Both retain their data across
container rebuilds on the same Docker/Podman engine. The image
initializes these directories with `vscode` ownership so new volumes are
writable. Existing volumes may need their ownership corrected separately.
Removing the volumes, resetting the Podman machine, or moving to a different
engine does not preserve them. Keep the same workspace location so the
devcontainer ID remains stable.

Before the **first rebuild** with these mounts, back up existing sessions:
new volumes do not automatically copy data from the old container. From a host
terminal, use `podman cp <old-container>:/home/vscode/.copilot ./copilot-backup`
and
`podman cp <old-container>:/home/vscode/.vscode-server/data/User ./vscode-user-backup`.
Keep these backups outside the repository; they may contain sensitive data.
After rebuilding, close VS Code and restore with
`podman cp ./copilot-backup/. <new-container>:/home/vscode/.copilot/`
and
`podman cp ./vscode-user-backup/. <new-container>:/home/vscode/.vscode-server/data/User/`.
If needed, restore ownership using
`podman exec --user root <new-container> chown -R vscode:vscode /home/vscode/.copilot /home/vscode/.vscode-server/data/User`
before reopening VS Code. Use `docker` instead of `podman` for Docker.

The default devcontainer always mounts a Podman socket and sets
`CONTAINER_HOST` and `KIND_EXPERIMENTAL_PROVIDER=podman`; there is no separate
opt-in configuration. Its default socket path is intended for a Podman machine
VM user with UID `501`. The container sets `KUBECONFIG` to
`/home/vscode/.kube/operatarr-e2e` and runs `kubectl get nodes` on startup
when the file is readable. A missing kubeconfig or unreachable cluster produces
a warning without blocking startup. The container does not automatically
create or mount Kubernetes credentials; back up the kubeconfig before
rebuilding and restore it afterwards. Ensure the cluster API is reachable from
the container. The e2e tests operate only on the configured kind cluster and
install resources into it; they do not create the cluster.

On a Linux host with rootless Podman, start the host's user socket with
`systemctl --user start podman.socket` and replace the existing socket mount in
`.devcontainer/devcontainer.json`, for example:

```jsonc
"mounts": [
  "source=${devcontainerId}-copilot,target=/home/vscode/.copilot,type=volume",
  "source=${devcontainerId}-vscode-user-data,target=/home/vscode/.vscode-server/data/User,type=volume",
  "source=${localEnv:XDG_RUNTIME_DIR}/podman/podman.sock,target=/tmp/host-podman.sock,type=bind"
]
```

Rebuild the devcontainer after changing its configuration. The host socket must
be accessible to the `vscode` user; depending on how the devcontainer is
launched, matching the host UID/GID may be necessary. The socket grants access
to the host user's Podman API and therefore control over their containers; do
not make it world-writable or expose it over an unauthenticated network.
Mounting the socket alone does not ensure kind's Kubernetes API endpoint is
reachable from the devcontainer. Verify that endpoint and host networking
before running e2e tests. This mount is specific to Linux rootless Podman;
other hosts and nested Podman need different configuration.

With a Podman machine (for example, on macOS), the default configuration mounts
the socket inside the VM:

1. Start the socket in the VM with
   `podman machine ssh systemctl --user start podman.socket`. To start it
   automatically with the VM, use `enable` instead of `start`.
2. Print the VM user's UID and socket path with
   `podman machine ssh 'id -u; ls -l "$XDG_RUNTIME_DIR/podman/podman.sock"'`.
   If the UID is not `501`, update the mount source in
   `.devcontainer/devcontainer.json`.
3. Open or rebuild the devcontainer using the default configuration.
4. Inside the container, verify the connection with `podman info` and
   `kind get clusters`.

This configuration disables SELinux labeling for the devcontainer and uses the
VM's host network so kind's API server on `127.0.0.1` is reachable. In a
rootless Podman machine, container root is the VM user that owns the socket and
`vscode` maps to a subordinate UID. On every start,
`.devcontainer/post-start.sh` adds an ACL entry granting only that UID access to
the mounted socket. Podman's `--userns=keep-id` is not used because VS Code
launches the container through the Docker CLI, which rejects that option even
when backed by Podman. If socket access still fails, rerun
`sh .devcontainer/post-start.sh` and verify that the UID in the socket mount
path and the Podman VM user's UID match. The same security warning applies: the devcontainer
can control every container in the Podman machine, including itself.

## License

This project is licensed under the Apache License 2.0. See the [LICENSE](LICENSE) file for details.

## Contact

Maintained by [Floris Feddema](https://github.com/FlorisFeddema).

---

*Please update this README with specific details about operatarr as your project evolves.*
