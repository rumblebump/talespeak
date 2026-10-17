# Server settings per environment

CI deploys write `~/talespeak/.env` on the server from one of these files, so a server's settings live in git and every deploy recreates them. No secrets here: values that must stay secret (later, for example an SMTP password) come from the GitHub environment's secrets.

| File | Server | Deployed by |
| --- | --- | --- |
| `dev.env` | Dev server, image `edge` | push to `dev`, when `DEV_DEPLOY` is `true` |
| `staging.env` | Public staging, image `main` | push to `main`, when `STAGING_DEPLOY` is `true` |
| `prod.env` | Production, release images | release tag, when `PROD_DEPLOY` is `true` |

The deploy adds `GH_OWNER` and `TALESPEAK_VERSION` (the image tag) itself. Plain `KEY=value` lines only, no inline comments.
