# Development setup guide

Two parts: working on your own machine, and the shared dev server that GitHub Actions deploys `main` to.

## Local development

Requirements: Go (version in `go.mod`), git, make. No C compiler, GPU or database server needed.

```sh
git clone https://github.com/OWNER/talespeak.git && cd talespeak
go run ./cmd/talespeak serve --data ./data --listen :8080   # http://localhost:8080
go test ./...
```

Content (YAML rooms, Lua rule packs, glossaries) lives in `content/` and reloads without a restart.

Optional AI sidecar locally:

```sh
docker run --rm -p 8081:8081 -v "$PWD/models:/models:ro" ghcr.io/ggml-org/llama.cpp:server \
  -m /models/model.gguf --host 0.0.0.0 --port 8081
TALESPEAK_LLM_URL=http://localhost:8081 go run ./cmd/talespeak serve --data ./data
```

## Dev server

The dev server runs the `edge` image, which CI builds from every green commit on `main`.

### 1. Prepare the server

Follow [setup-server.md](setup-server.md) steps 1 to 4 with these `.env` values:

```sh
TALESPEAK_DOMAIN=dev.talespeak.example.org
TALESPEAK_VERSION=edge
```

Keep the files in `~/talespeak`; the deploy job runs `cd ~/talespeak && docker compose pull talespeak && docker compose up -d`.

### 2. Create a deploy user and key

On the server:

```sh
sudo adduser --disabled-password deploy
sudo usermod -aG docker deploy
sudo -u deploy mkdir -p ~deploy/.ssh
```

On your machine, create a key used only by GitHub Actions:

```sh
ssh-keygen -t ed25519 -N "" -C "github-actions-dev-deploy" -f ./dev_deploy_key
```

Put `dev_deploy_key.pub` into `~deploy/.ssh/authorized_keys` on the server, then move the `~/talespeak` folder into the deploy user's home.

Note: membership in the `docker` group is effectively root on that machine. Use a server dedicated to the dev instance.

### 3. Add GitHub secrets

In the repository, **Settings → Environments → New environment** named `dev`, then add these secrets:

| Secret | Value |
| --- | --- |
| `DEV_SSH_HOST` | Server hostname or IP |
| `DEV_SSH_USER` | `deploy` |
| `DEV_SSH_KEY` | Contents of `dev_deploy_key` (the private key) |
| `DEV_SSH_KNOWN_HOSTS` | Output of `ssh-keyscan -t ed25519 <host>` |

Delete the local private key file afterwards. Never paste any of these values into a protocol file, issue or commit.

### 4. Make the image pullable

The first push creates `ghcr.io/OWNER/talespeak`. Set the package to **public** (GitHub → Packages → talespeak → Package settings), or log the server in with a read-only token:

```sh
echo "$GHCR_READ_TOKEN" | docker login ghcr.io -u OWNER --password-stdin
```

### 5. Check a deploy

Push a code change to `main`, then watch **Actions → ci → deploy-dev**. Log the check in `protocol/` as described in [protocol/README.md](../protocol/README.md).
