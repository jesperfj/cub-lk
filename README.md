# cub-lk

`lk` is a [`cub`](https://docs.confighub.com) plugin that brings up a local
Kubernetes cluster (kind) and wires it into ConfigHub as a worker + target
with a single command.

> **Status:** v0 scaffold. The commands print what they *would* do but do not
> create or delete anything yet. The goal of v0 is to validate the plugin
> distribution + install path end-to-end.

## Install

```
cub plugin install jesperfj/cub-lk
```

This downloads the latest release asset matching your OS/arch from
[GitHub Releases](https://github.com/jesperfj/cub-lk/releases) and places it
at `~/.confighub/plugins/lk`.

Verify:

```
cub plugin list
cub lk --help
```

## Commands

```
cub lk up [--name <cluster>]      Bring up a kind cluster and connect it to ConfigHub
cub lk down [--name <cluster>]    Delete the kind cluster
cub lk list                       List clusters managed by lk
cub lk version                    Print plugin version
```

## Development

```
go build ./...
go test ./...
go run . up --name dev
```

Install the locally-built binary as a plugin:

```
go build -o ~/.confighub/plugins/lk .
cub lk version
```

## Releasing

Push a tag matching `v*`:

```
git tag v0.1.0
git push origin v0.1.0
```

The `release` workflow builds darwin/linux × amd64/arm64 binaries named
`cub-lk-<os>-<arch>` and attaches them (plus `.sha256` files) to a GitHub
release. `cub plugin install` matches assets by OS/arch tokens in the asset
name, so this naming is what the CLI expects.
