# CI/CD plan

Only the jobs a change needs run; docs-only commits finish in well under a minute.

## Workflows

| Workflow | Trigger | What runs |
| --- | --- | --- |
| `ci.yml` | Every push to `main`, every pull request | `changes` and `secrets` always; `go`, `web`, `image` and `deploy-dev` only when relevant files changed |
| `release.yml` | Pushing a tag `v*.*.*` | Tests, multi-arch image, binaries, GitHub release |

## What triggers what in `ci.yml`

| Changed files | changes | secrets | go | web | image | deploy-dev |
| --- | --- | --- | --- | --- | --- | --- |
| Only `*.md`, `docs/`, `tasks/`, `protocol/` | yes | yes | no | no | no | no |
| `**/*.go`, `go.mod`, `go.sum`, `content/**` | yes | yes | yes | no | yes | main only |
| `web/**` | yes | yes | no | yes | yes | main only |
| `Dockerfile` | yes | yes | no | no | yes | main only |
| `deploy/**` | yes | yes | no | no | no | no (copy to the server by hand) |

- **No Go module yet:** `go` and `image` are skipped until `go.mod` exists (T-0002), because the Docker build needs it.
- **`ci-ok`** always reports, and is the single required status check for branch protection. Filtering at the trigger level instead would leave required checks waiting forever on docs-only pull requests.
- **`secrets`** runs gitleaks over the pushed commits on every push, because protocol files with commands are where a token would most likely slip in.
- **Concurrency:** a new push to the same branch cancels the older run. Deploys never cancel each other.

## Efficiency choices

- **One static binary, distroless image.** `CGO_ENABLED=0` with the pure-Go SQLite driver, web client embedded via `go:embed`. Expected image size is roughly 20–40 MB.
- **No QEMU.** The Dockerfile cross-compiles for arm64 on the amd64 runner (`--platform=$BUILDPLATFORM`), which is much faster than emulation.
- **Caching:** Go module and build cache via `setup-go`; Docker layer cache via the GitHub Actions cache (`type=gha`).
- **Pull requests build amd64 only, without pushing**, to catch Dockerfile breakage cheaply. Only `main` builds both architectures and pushes.

## Image tags

| Tag | Built from | Used by |
| --- | --- | --- |
| `edge`, `sha-<commit>` | Every green `main` commit with code changes | Dev server |
| `0.3.1`, `0.3`, `latest` | Release tags | Users |

## Making a release

```sh
git tag v0.1.0
git push origin v0.1.0
```

`release.yml` then publishes `ghcr.io/OWNER/talespeak:0.1.0` for amd64 and arm64, plus binaries for Linux (amd64, arm64), macOS (arm64) and Windows (amd64) with a `SHA256SUMS` file, and generates release notes from merged pull requests.

## To do when the repo exists

- Bump action versions to their current majors.
- Set branch protection on `main` with `ci-ok` as the required check.
- Create the `dev` environment and its secrets ([setup-dev.md](setup-dev.md)).
