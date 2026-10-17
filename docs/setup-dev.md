# Development setup guide

Two parts: working on your own machine, and the shared servers GitHub Actions deploys to: the dev server (branch `dev`) and the public staging server (branch `main`). Branches and checks are described in [ci-cd.md](ci-cd.md).

## Local development

Requirements: Go (version in `go.mod`), git, make. No C compiler, GPU or database server needed.

```sh
git clone https://github.com/OWNER/talespeak.git && cd talespeak   # checks out dev
go run ./cmd/talespeak serve --data ./data --listen :8080   # http://localhost:8080
go test -short ./...   # quick, like CI on dev
```

Work on a branch from `dev` and open the pull request into `dev`. Tests are described in [testing.md](testing.md).

Content (YAML rooms, Lua rule packs, glossaries) lives in `content/` and reloads without a restart.

Optional AI sidecar locally:

```sh
docker run --rm -p 8081:8081 -v "$PWD/models:/models:ro" ghcr.io/ggml-org/llama.cpp:server \
  -m /models/model.gguf --host 0.0.0.0 --port 8081
TALESPEAK_LLM_URL=http://localhost:8081 go run ./cmd/talespeak serve --data ./data
```

## Dev and staging servers

Both are set up the same way; only the domain, image tag and GitHub environment differ.

| Server | Image tag | Built from | GitHub environment | Turned on by |
| --- | --- | --- | --- | --- |
| Dev | `edge` | every green push to `dev` | `dev` | variable `DEV_DEPLOY=true` |
| Staging (public, may break) | `main` | every green push to `main` | `staging` | variable `STAGING_DEPLOY=true` |

Suggested size with the AI sidecar: 4 vCPU, 8 GB RAM, 80 GB SSD; without it, 2 vCPU, 2 GB RAM, 40 GB.

### 1. Prepare the server

Install Docker ([setup-server.md](setup-server.md) step 1) and point the domain's DNS at the server. Nothing else: every deploy copies `compose.yml`, `Caddyfile` and a generated `.env` into `~/talespeak` of the deploy user, then runs `docker compose up` and waits for the health check. Server settings (domain, kids mode) live in git in [deploy/env/](../deploy/env/README.md); edit them there, never on the server.

### 2. Create a deploy user and key

On the server:

```sh
sudo adduser --disabled-password deploy
sudo usermod -aG docker deploy
sudo -u deploy mkdir -p ~deploy/.ssh
```

On your machine, create a key used only by GitHub Actions:

```sh
ssh-keygen -t ed25519 -N "" -C "github-actions-dev-deploy" -f ./dev_deploy_key   # one key per server
```

Put `dev_deploy_key.pub` into `~deploy/.ssh/authorized_keys` on the server.

Note: membership in the `docker` group is effectively root on that machine. Use a server dedicated to the dev or staging instance.

### 3. Add GitHub secrets

In the repository, **Settings → Environments → New environment** named `dev` (or `staging`), then add these secrets to it:

| Secret | Value |
| --- | --- |
| `DEPLOY_SSH_HOST` | Server hostname or IP |
| `DEPLOY_SSH_USER` | `deploy` |
| `DEPLOY_SSH_KEY` | Contents of `dev_deploy_key` (the private key) |
| `DEPLOY_SSH_KNOWN_HOSTS` | Output of `ssh-keyscan -t ed25519 <host>` |

Then set the repository variable (**Settings → Secrets and variables → Actions → Variables**) `DEV_DEPLOY` or `STAGING_DEPLOY` to `true`. Delete the local private key file afterwards. Never paste any of these values into a protocol file, issue or commit.

### 4. Image access

Nothing to do. The deploy logs the server into ghcr.io with the job's short-lived token, pulls, and logs out again, so the package can stay private.

### 5. Check a deploy

Push a code change to `dev` (or merge a release PR into `main` for staging), then watch **Actions → ci → deploy**. The job fails unless the containers are healthy on the server and `https://<domain>/healthz` reports the commit just built. Log the check in `protocol/` as described in [protocol/README.md](../protocol/README.md).
