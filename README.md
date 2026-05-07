# cub-lk

`lk` is a [`cub`](https://docs.confighub.com) plugin that brings up a local
Kubernetes cluster ([kind](https://kind.sigs.k8s.io)) and wires it into
ConfigHub as a Space + Worker + Target + worker-config Unit, with a single
command. The intended audience is anyone who wants a fresh, fully-managed
local cluster connected to ConfigHub for development, demos, debugging,
or trying out things like ArgoCD without manual setup.

```
$ cub lk up
Generating cluster name...
Creating kind cluster "fur-ball" ...
Creating ConfigHub space "fur-ball-cluster"...
Creating ConfigHub worker "worker" (Kubernetes provider)...
Generating worker manifest (cub worker install --export --include-secret)...
Creating ConfigHub target "target" bound to worker "worker"...
Storing worker manifest as Unit "worker-config"...
Applying worker manifest to cluster (kubectl --kubeconfig ... apply)...

Done.
  cluster:    fur-ball
  kubeconfig: ~/.confighub/lk/fur-ball.kubeconfig
  context:    kind-fur-ball
  space:      fur-ball-cluster
  worker:     fur-ball-cluster/worker
  target:     fur-ball-cluster/target

Port mappings (host → NodePort):
  localhost:30010 → nodePort 30010
  ...
  localhost:30019 → nodePort 30019
```

## Requirements

- [`cub`](https://docs.confighub.com) on `PATH` and authenticated (`cub auth login`).
- [`kind`](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) on `PATH`.
- [`kubectl`](https://kubernetes.io/docs/tasks/tools/) on `PATH`.
- A running Docker engine (Docker Desktop on macOS/Windows, or Docker / Podman on Linux).
- macOS or Windows (Docker Desktop) is the supported target. Linux mostly works but the localhost-server URL rewrite (see below) needs an extra docker config that lk doesn't wire up yet.

## Install

```
cub plugin install jesperfj/cub-lk
```

This downloads the latest GitHub release asset matching your OS/arch and
installs it at `~/.confighub/plugins/lk`. To upgrade:

```
cub plugin install jesperfj/cub-lk --force
```

To pin a specific version:

```
cub plugin install jesperfj/cub-lk@v0.5.0 --force
```

Verify:

```
cub plugin list
cub lk version
```

## Commands

```
cub lk up [flags]                 Bring up a kind cluster and wire it into ConfigHub
cub lk down --name <cluster>      Tear down a cluster (kind + ConfigHub space)
cub lk list                       List clusters tracked by lk
cub lk version                    Print plugin version
```

### `cub lk up`

```
--name string         cluster name (auto-generated if empty)
--space string        ConfigHub space slug (defaults to <name>-cluster)
--namespace string    Kubernetes namespace for the worker target binding (default "confighub")
--mount string[]      host:container bind mount (repeatable; container path defaults to /mnt/<basename>)
--no-unit             skip creating the worker-config Unit in ConfigHub
--no-ports            skip default localhost:30000-30009 port mappings
```

What it does, in order:

1. Picks a cluster name (auto-generated via `cubbyname.Random()` if `--name` is omitted).
2. Probes `docker ps` for currently-bound host ports; reserves the first free 10-port window inside `30000-30099`.
3. Creates the kind cluster with that port window mapped on the control-plane node and a dedicated kubeconfig at `$CUB_CONFIG/lk/<name>.kubeconfig` — never merged into `~/.kube/config`.
4. Creates a ConfigHub Space (`<name>-cluster` by default), tagged with `Labels.cub-lk=true` and a set of `ijn.me/cub-lk-*` annotations (cluster name, host, port range).
5. Creates a `BridgeWorker` in the Space with `Kubernetes/YAML` provider declared upfront — so target binding doesn't have to wait for the worker pod to connect.
6. Generates the worker Kubernetes manifest by shelling out to `cub worker install --export --include-secret`. If the cub server is on localhost, rewrites `CONFIGHUB_URL` to `host.docker.internal:<port>` so the pod (running inside docker) can reach the host.
7. Creates a `Target` bound to the worker, with `KubeContext` pointing at the new kind context.
8. Stores the worker manifest as a `Unit` (`worker-config`) in the Space, bound to the target. (Skipped with `--no-unit`.)
9. `kubectl apply`s the manifest into the cluster.

If any step fails, lk rolls back what it created (kind cluster, then `cub space delete --recursive`). lk does not write a state file; everything it needs to know on subsequent commands is derived from the kubeconfig file at `$CUB_CONFIG/lk/<name>.kubeconfig` plus the Space's annotations in ConfigHub.

### `cub lk down --name <cluster>`

```
--name string   cluster name (required)
--force         delete the local kind cluster + kubeconfig even if no
                matching ConfigHub Space is found in the current context
```

1. Looks up the cluster's Space in the current cub context (matching by `Annotations.ijn.me/cub-lk-cluster-name`).
2. If no matching Space is found, fails with a "are you in the right context?" hint. Pass `--force` to skip the ConfigHub side and just delete the local kind cluster + kubeconfig.
3. Deletes the kind cluster — this stops the worker pod, dropping its connection to ConfigHub. Necessary because cub refuses to delete a Connected worker.
4. Deletes the ConfigHub Space recursively (cascades Unit, Target, Worker), retrying for up to 30s while the worker connection clears.
5. Removes the dedicated kubeconfig file.

### `cub lk list`

Shows the union of:
- Local kubeconfig files at `$CUB_CONFIG/lk/*.kubeconfig` (clusters lk created on this host)
- ConfigHub Spaces in the current cub context with `Labels.cub-lk = 'true'` and `Annotations.ijn.me/cub-lk-host = <my-hostname>`

Cross-references with `kind get clusters` to flag drift.

```
NAME        SPACE               PORTS        KUBECONFIG                                         STATUS
lk-a        lk-a-cluster        30010-30019  /Users/jesper/.confighub/lk/lk-a.kubeconfig        Ready
ghrfw       -                   -            /Users/jesper/.confighub/lk/ghrfw.kubeconfig       Local only (no Space in current context)
lk-stale    lk-stale-cluster    30020-30029  /Users/jesper/.confighub/lk/lk-stale.kubeconfig    Drift: kind cluster missing
```

The STATUS column reports any drift transparently:

| Status | Meaning |
|---|---|
| `Ready` | local kubeconfig + kind cluster + matching Space |
| `Local only (no Space in current context)` | local kubeconfig + kind cluster, but no Space here — likely registered against a different cub context |
| `Drift: kind cluster missing` | local kubeconfig + Space, but the kind cluster is gone (someone ran `kind delete cluster` directly) |
| `Stale kubeconfig` | local kubeconfig only — both kind cluster and Space are gone |
| `ConfigHub only` / `Stranded` | other partial-cleanup combinations |

### `cub lk version`

Prints the plugin version + commit + build date.

## Concepts

### Per-cluster kubeconfig

Each cluster's credentials live in `$CUB_CONFIG/lk/<name>.kubeconfig` — never merged into `~/.kube/config`. Use the cluster like:

```
KUBECONFIG=$(cub lk list | awk '$1=="lk-a"{print $4}') kubectl get pods -A
```

…or just export it for the duration of a shell:

```
export KUBECONFIG=~/.confighub/lk/lk-a.kubeconfig
kubectl get pods -A
```

### Port mappings

Each lk cluster opens a 10-port window on the host, mapped 1:1 to NodePorts on the control-plane container. Default search range is `30000-30099`; lk skips any range already bound by another docker container, so multiple lk clusters and other docker services coexist.

To expose a Kubernetes Service via `localhost:<port>`, set its `nodePort` to a port in your cluster's window:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: argocd-server
  namespace: argocd
spec:
  type: NodePort
  ports:
  - name: https
    port: 443
    targetPort: 8080
    nodePort: 30010      # → reachable as https://localhost:30010
```

Use `--no-ports` to skip the default range entirely (handy when you want lk to coexist with another tool that owns 30000-30099, or when you don't need any localhost forwarding).

### ConfigHub annotations

Every Space lk creates carries:

| Key | Example |
|---|---|
| `Labels.cub-lk` | `"true"` (queryable marker) |
| `Annotations.ijn.me/cub-lk-cluster-name` | `fur-ball` |
| `Annotations.ijn.me/cub-lk-port-range` | `30010-30019` |
| `Annotations.ijn.me/cub-lk-host` | `MacBook-Pro.local` |

Find all your lk clusters across the org:

```
cub space list --where "Labels.cub-lk = 'true'"
```

(Currently `--where Annotations.X` is not supported by cub; tracked in [confighubai/confighub#4346](https://github.com/confighubai/confighub/issues/4346). Annotations are still visible via `cub space get -o yaml` and the UI.)

### Host filesystem mounts

`--mount HOST[:CONTAINER]` (repeatable) bind-mounts a host directory into the kind node. Pods can then `hostPath`-mount it for live local development:

```
cub lk up --mount ../my-function-worker
```

Container path defaults to `/mnt/<basename>` if not specified. Tilde and relative host paths are expanded to absolute. The host path must exist.

In a Pod / Deployment:

```yaml
spec:
  containers:
  - name: app
    image: my-image
    volumeMounts:
    - name: src
      mountPath: /app
  volumes:
  - name: src
    hostPath:
      path: /mnt/my-function-worker
```

Edit code on the host; the pod sees the changes immediately. Useful for iterative work on custom function workers, bridges, etc.

### Localhost cub server

When your active cub context points at a localhost address (`localhost`, `127.0.0.1`, `0.0.0.0`, `::1`), lk rewrites the worker pod's `CONFIGHUB_URL` to `host.docker.internal:<port>`. This works on Docker Desktop (macOS, Windows) out of the box. On Linux you'd need `--add-host=host.docker.internal:host-gateway` on the kind container, which lk doesn't set up yet.

To target a different cub server, switch context first:

```
cub --context local lk up
```

`cub` forwards the active context's server URL and token to the plugin via env. There's no separate `--server` flag on `lk` itself — context selection is the supported mechanism.

### No state file

There is no `state.yaml`. lk derives everything it needs at command time from two authoritative sources:

- Per-cluster kubeconfig files at `$CUB_CONFIG/lk/<name>.kubeconfig` — the marker that says "lk created this cluster on this host" (created by `lk up`, removed by `lk down`).
- ConfigHub Spaces in the current cub context with `Labels.cub-lk = 'true'` and the `ijn.me/cub-lk-host` annotation matching this hostname.

This means lk can never get out of sync with itself. Drift between local and ConfigHub state is visible in `lk list`'s STATUS column and is recoverable manually:

- Lk-managed Space without local kind cluster → `cub space delete <slug> --recursive`
- Local kubeconfig + kind cluster without matching Space → `cub lk down --name <n> --force` (or just `kind delete cluster --name <n>` and `rm <kubeconfig>`).
- Find lk-managed Spaces across your cub org with `cub space list --where "Labels.cub-lk = 'true'"`.

## Common workflows

### Run ArgoCD locally

```
cub lk up --name argo
KUBECONFIG=~/.confighub/lk/argo.kubeconfig
kubectl create namespace argocd
kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
kubectl patch svc argocd-server -n argocd --patch '{"spec":{"type":"NodePort","ports":[{"name":"https","port":443,"targetPort":8080,"nodePort":30010}]}}'
kubectl -n argocd patch cm argocd-cmd-params-cm --patch '{"data":{"server.insecure":"true"}}'
kubectl -n argocd rollout restart deploy/argocd-server
# Get the initial admin password
kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath="{.data.password}" | base64 -d
# Visit http://localhost:30010 (the actual port lk allocated — see `cub lk list`)
```

### Live local-dev for a custom worker

```
cub lk up --name dev --mount ../my-function-worker
# Author a Deployment in your space with hostPath /mnt/my-function-worker
# Edit code on the host; iterate without rebuilds
```

### Debug against a localhost cub

```
# Assumes you have a cub context pointing at http://localhost:9090
cub --context local lk up
# Worker pod CONFIGHUB_URL is auto-rewritten to host.docker.internal:9090
```

### Multiple clusters side-by-side

```
cub lk up --name a   # gets ports 30010-30019 (or whatever's free)
cub lk up --name b   # gets the next free 10-port window
cub lk list
```

## Limitations / known gotchas

- **Linux + localhost cub server:** lk doesn't currently set `--add-host=host.docker.internal:host-gateway` on the kind container, so the URL rewrite to `host.docker.internal` won't resolve from inside the pod. Workaround: use a non-localhost hostname or set the kind config manually.
- **Port range ceiling:** if all of `30000-30099` is taken, `lk up` fails with a clear error. Free up some ports or use `--no-ports`.
- **No multi-node clusters:** kind only spins up a single control-plane node. If you need multi-node topologies, this isn't (yet) the right tool.
- **Worker delete during teardown takes a few seconds** while waiting for the pod's connection to drop after the kind cluster goes away. The retry loop handles this transparently.
- **`cub --where Annotations.X`** isn't supported today, so the structured per-cluster annotations aren't directly queryable. The `Labels.cub-lk` marker covers the "show me all my lk clusters" case. See [confighubai/confighub#4346](https://github.com/confighubai/confighub/issues/4346).

## Architecture

```
cmd/                    # cobra commands (up, down, list, version)
internal/cubclient/     # SDK wrapper: env-driven OpenAPI client + space/worker/target/unit ops
internal/kindcli/       # shell-outs to kind, kubectl, and `cub worker install --export`
internal/docker/        # docker ps probe + free-port-window picker
internal/state/         # kubeconfig path conventions + LkClusterNames() listing
```

Notes on what the SDK does vs what lk shells out to:

- **SDK (`github.com/confighub/sdk/core`)** is used for all ConfigHub API CRUD: list/create/delete spaces, create workers (with declared `SupportedConfigTypes`), create targets, create units. Auth is `Authorization: Bearer $CUB_TOKEN` against `$CUB_SERVER/api`.
- **`cub` is shelled out** only for `cub worker install --export --include-secret`, which is a 250-line manifest generator inside cub's `package main`. Re-implementing it in lk would mean tracking cub-internal logic over time. Lifting it into the SDK is filed as [confighubai/confighub#4345](https://github.com/confighubai/confighub/issues/4345).
- **`kind` and `kubectl`** are shelled out for cluster lifecycle and applying the manifest.
- **`docker ps`** is shelled out for port-collision detection.

## Related issues filed against ConfigHub

- [#4344](https://github.com/confighubai/confighub/issues/4344) — `CUB_CONFIG` env var: docs say directory, code reads as file, breaks plugins that shell out to `cub`.
- [#4345](https://github.com/confighubai/confighub/issues/4345) — SDK extraction candidates surfaced building lk (`cubapi.NewPluginClient`, `NewSpacePrefix`, worker-install manifest generator).
- [#4346](https://github.com/confighubai/confighub/issues/4346) — `cub --where Annotations.X` not supported.

## Development

```
go build ./...
go test ./...
```

Install the locally-built binary as a plugin (overwrites the released one):

```
go build -o ~/.confighub/plugins/lk .
cub lk version
```

Re-install the released version after local testing:

```
cub plugin install jesperfj/cub-lk --force
```

## Releasing

Push a `v*` tag:

```
git tag v0.X.Y
git push origin v0.X.Y
```

The `release` workflow runs tests, then builds darwin/linux × amd64/arm64 binaries named `cub-lk-<os>-<arch>` and attaches them (plus `.sha256` files) to a GitHub release. `cub plugin install` matches assets by OS/arch tokens in the asset name.

## License

[MIT](LICENSE).

