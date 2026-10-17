# CI/CD plan

Branches decide how much runs. Pull requests into `dev` get quick checks, everything merged into `dev` gets the full tests for what changed, and a release gets everything.

## Branches and environments

| Branch or ref | Purpose | Checks | Image tag | Deploys to |
| --- | --- | --- | --- | --- |
| `dev` (default branch) | Day-to-day work; feature PRs and Renovate PRs target it | Full, for what changed | `edge` | Dev server |
| PR into `dev` | Feature, fix, dependency update | Quick, on the merge result | none | none |
| PR `dev` → `main` | Release candidate | Full, on the merge result | built and scanned, not pushed | none |
| `main` | Released code | Full | `main` | Public staging (may break) |
| Tag `v1.2.3` on `main` | A release | Release tests and scans | `1.2.3`, `1.2`, `latest` | Production (later) |

```text
feature/x ──PR──▶ dev ──release PR──▶ main ──tag v0.3.0──▶ release
   quick checks    │ edge image         │ full checks        │ image + binaries
                   ▼                    ▼                    ▼
               dev server        staging server       production (later)
```

Pull request runs test the merge result GitHub creates (`refs/pull/N/merge`), so a branch that is fine alone but breaks after merging fails its checks.

## What runs where (`ci.yml`)

| Job | PR into `dev` | Push to `dev` | PR into `main`, push to `main` |
| --- | --- | --- | --- |
| `changes` | always | always | always |
| `secrets` (gitleaks) | always | always | always |
| `lint-ci` (actionlint, Renovate config) | when `.github/` or `renovate.json` changed | same | same |
| `go` | when Go or content changed: `go vet`, `go test -short`, build | when Go or content changed: `go vet`, `go test -race` with coverage, short fuzz runs, govulncheck, build | always, full like a push to `dev` |
| `web` | when `web/` or `e2e/` changed | same | same |
| `e2e` (Playwright) | no | when Go, content or web changed | always |
| `trivy-fs` | when `go.mod`, `go.sum`, `Dockerfile`, `deploy/`, `web/vendor/` or `e2e/package*.json` changed | same | always |
| `image` | no | with code changes: Trivy scan, then amd64 + arm64 pushed as `edge` | always: Trivy scan; on push to `main` also pushed as `main` |
| `deploy` | no | dev server, when `DEV_DEPLOY` is `true` | push to `main`: staging, when `STAGING_DEPLOY` is `true` |
| `ci-ok` | always | always | always |

- **Docs-only changes** (`*.md`, `docs/`, `tasks/`, `protocol/`) run only `changes`, `secrets` and `ci-ok`, and finish in well under a minute.
- **Not built yet:** `go`, `image` and `e2e` are skipped until `go.mod` exists (T-0002); `e2e` also waits for `e2e/package.json` (T-0015).
- **`ci-ok`** always reports, and is the single required status check. Filtering at the trigger level instead would leave required checks waiting forever on docs-only pull requests.
- **Concurrency:** a new push to the same branch cancels the older run. Deploys never cancel each other.

## Security scanning

- **gitleaks** on every push and pull request, because protocol files with commands are where a token would most likely slip in.
- **Trivy** scans the repository files (dependencies, secrets, misconfigurations such as Dockerfile issues) and every image before it is pushed. HIGH and CRITICAL findings with a fix available fail the run. Use `aquasecurity/trivy-action` 0.35.0 or newer only: older tags were hijacked in March 2026 ([GHSA-69fq-xp46-6x23](https://osv.dev/vulnerability/GHSA-69fq-xp46-6x23)).
- **govulncheck** reports known vulnerabilities only in Go code paths that are actually called.
- **Release images** carry an SBOM and build provenance.

## Dependency updates (Renovate)

`renovate.json` configures the free [Mend Renovate GitHub App](https://github.com/apps/renovate). Install it once for the repository; there is nothing to host.

- Update PRs target the default branch (`dev`) and get the quick checks like any other PR.
- Non-major updates are grouped (GitHub Actions, Go modules, CI tools) and arrive on Monday mornings; security fixes come at any time.
- Actions are pinned to commit digests, so a moved tag can never change what runs.
- Tool versions in workflows and the Dockerfile carry a `# renovate: datasource=... depName=...` comment so Renovate can bump them too.
- The Dependency Dashboard issue lists everything pending.

## Efficiency choices

- **One static binary, distroless image.** `CGO_ENABLED=0` with the pure-Go SQLite driver, web client embedded via `go:embed`. Expected image size is roughly 20–40 MB.
- **No QEMU.** The Dockerfile cross-compiles for arm64 on the amd64 runner (`--platform=$BUILDPLATFORM`), which is much faster than emulation.
- **Caching:** Go module and build cache via `setup-go`; Docker layer cache via the GitHub Actions cache (`type=gha`); npm cache for Playwright.
- **Feature PRs build no image.** Only pushes and release PRs do, and the multi-arch build reuses the cached layers of the scanned amd64 build.

## Making a release

1. Open a pull request from `dev` into `main` and wait for `ci-ok`.
2. Merge it. `main` deploys to staging; check it there.
3. Tag the merge commit on `main`:

```sh
git switch main && git pull
git tag v0.1.0
git push origin v0.1.0
```

`release.yml` refuses tags that are not on `main`, runs the tests, scans and publishes `ghcr.io/OWNER/talespeak:0.1.0` for amd64 and arm64, uploads binaries for Linux (amd64, arm64), macOS (arm64) and Windows (amd64) with a `SHA256SUMS` file, and generates release notes from merged pull requests. Once `PROD_DEPLOY` is `true`, it deploys the version to production after a reviewer approves the `prod` environment.

## Repository settings

| Setting | Value |
| --- | --- |
| Default branch | `dev` |
| Branch protection on `dev` and `main` | Require a pull request, require the `ci-ok` check, no force pushes |
| Environments | `dev`, `staging`, `prod` (with a required reviewer), each with the `DEPLOY_SSH_*` secrets from [setup-dev.md](setup-dev.md) |
| Repository variables | `DEV_DEPLOY`, `STAGING_DEPLOY`, `PROD_DEPLOY`: set to `true` once that server exists |
| Renovate | Install the Renovate GitHub App for this repository |
