#!/bin/sh
set -eu

socket=/tmp/host-podman.sock

# Host networking keeps the Podman VM's hostname without an /etc/hosts entry,
# which makes sudo warn on every call.
host=$(hostname)
if ! getent hosts "$host" >/dev/null; then
    echo "127.0.0.1 $host" | sudo tee -a /etc/hosts >/dev/null
fi

# Container root is the rootless Podman VM user that owns the socket, while
# vscode maps to a subordinate UID. Grant only that UID access instead of
# relying on Podman-only --userns=keep-id, which the Docker CLI rejects.
if [ -S "$socket" ]; then
    sudo setfacl -m "u:$(id -u):rw" "$socket"
else
    echo "warning: $socket is not mounted; podman and kind will be unavailable" >&2
fi

export KUBECONFIG="$HOME/.kube/operatarr-e2e"
if [ ! -f "$KUBECONFIG" ] || [ ! -r "$KUBECONFIG" ]; then
    echo "warning: kind kubeconfig is missing or unreadable: $KUBECONFIG" >&2
elif ! kubectl get nodes --request-timeout=10s; then
    echo "warning: unable to list kind cluster nodes; check the kubeconfig and cluster connectivity" >&2
fi
