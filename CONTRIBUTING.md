# Contributing to Talespeak

Thanks for helping. The base engine is free forever, and improvements are most useful here, upstream, where every server gets them.

## Branches

- `dev` is the default branch. Branch from it and open your pull request into `dev`.
- `main` holds released code. Only release pull requests from `dev` go into `main`, and releases are tagged on `main`.
- Details, and what CI runs for each: [docs/ci-cd.md](docs/ci-cd.md).

## Pull requests

- One topic per pull request, with its tests ([docs/testing.md](docs/testing.md)).
- Link the task (`T-0012`) in the description, and move the task file between `tasks/open`, `tasks/doing` and `tasks/done` ([tasks/README.md](tasks/README.md)).
- Manual or blackbox checks go into `protocol/` with the exact commands. Never write secrets: use `$VARIABLES` ([protocol/README.md](protocol/README.md)).
- CI must be green (`ci-ok`) before merging.
- No external URLs in the web client or in packs: everything is served by the Talespeak server itself.

## Sign-off (DCO)

Every commit needs a `Signed-off-by` line, which certifies the [Developer Certificate of Origin](https://developercertificate.org/): you wrote the change or have the right to submit it under the project's license.

```sh
git commit -s -m "Add timer pause for GMs"
```

There is no CLA. Engine contributions are accepted under AGPL-3.0-or-later with the [Talespeak Content Exception](LICENSE-EXCEPTION.md); see [docs/licensing.md](docs/licensing.md).
