# Testing

Every feature ships with its tests in the same pull request. Quick tests run on pull requests into `dev`; the full set runs on everything merged into `dev` and on release PRs into `main` ([ci-cd.md](ci-cd.md)). Manual and blackbox checks against a server are logged in [protocol/](../protocol/README.md).

## Layers

| Layer | Where | Runs | What it covers |
| --- | --- | --- | --- |
| Unit | `*_test.go` next to the code | Quick and full | Pure logic: command parser, dice and rules helpers, chat filter, Whisp matching, permission checks, pack manifest and version ranges |
| Integration | `*_test.go`, skipped with `-short` | Full | Real SQLite in a temp dir, store migrations, world loading, timers surviving a restart, outbox mail against a fake SMTP server |
| Lua pack tests | `content/<pack>/tests/*_test.lua` | Quick and full | Pack authors test their own rooms, dialogs and rules through the public Lua API, with `talespeak test content/<pack>` |
| API and WebSocket blackbox | `internal/e2e/` with `httptest` | Full | Sign-in, registration modes, roles and edu safety rules, world visibility and join codes, chat levels, from the outside only |
| Fuzz | `Fuzz*` functions | Full (20 s each) | Text normalizer and filter, command parser, YAML and Lua pack loading, join code input |
| Benchmarks | `Benchmark*` functions | By hand, logged in protocol/ | Memory and CPU per world, Whisp latency, messages per second |
| Browser (Playwright) | `e2e/` | Full | The real web client against a real server binary |

## Go conventions

- Table-driven tests, standard `testing` package only; no assertion library needed.
- Mark slow tests with `if testing.Short() { t.Skip("integration") }`.
- **Time is injected.** The engine reads time through a `Clock` interface, so tests for timers, deadlines, rate limits and code expiry move a fake clock instead of sleeping.
- **No network.** Tests never reach the internet. The LLM sidecar, SMTP and later SSO are replaced by local fakes (`httptest.Server`, and for SMTP the in-process server of `internal/mail/mailtest`, with STARTTLS, TLS and a certificate made at start).
- **Golden files** in `testdata/` for rendered room text and widget messages; update them with `go test ./... -update`.
- The race detector runs on every full run, because worlds, timers and WebSocket sessions are concurrent.
- Coverage is reported on full runs. There is no hard threshold; a new package without tests is flagged in review.

## Browser tests (Playwright)

The client is plain JavaScript, so the tests are plain JavaScript too, with `@playwright/test`.

```sh
CGO_ENABLED=0 go build -o bin/talespeak ./cmd/talespeak
cd e2e
npm ci
npx playwright install chromium
npx playwright test                  # starts ../bin/talespeak three times: the test pack, the demo, and the demo for party tests
npx playwright test --ui             # watch and debug locally
```

- Two projects, both Chromium: `chromium` (desktop) and `phone` (Pixel 7 size and touch).
- Tests on one server share its world, and run side by side: they must not depend on being alone. `open()` in `fixtures.js` signs every test up with an account of its own (`traveler_` and a random suffix). Tests where players meet (`party.spec.js`) get a demo server of their own, run one at a time and only on `chromium`, so no other test takes the horn they want.
- Import `test` and `expect` from `e2e/tests/fixtures.js`: its automatic `guard` fixture is the offline and CSP guard below.

- The server starts from a fresh data directory with `content/test-pack` (`TALESPEAK_PACK`), a tiny pack with every renderer once; T-0015 adds a second room, a timer and a soundboard. The connect burst is raised for the tests, since every test connects from 127.0.0.1.
- Chromium on every full run. Firefox and WebKit later, before releases.
- **Offline and CSP guard:** every test fails if the page requests anything from another origin, or if the browser reports a Content-Security-Policy violation. This keeps the offline promise tested on every release.
- Timers are tested with Playwright's clock control (`page.clock`), not by waiting.
- Accessibility smoke checks with axe-core on the main screens (large text, keyboard only, screen reader labels).
- First scenarios: register and sign in per server mode; edu kid waits in the lobby until invited; GM invites a kid; dialog with a deadline picks the default when time runs out; secret world needs its code and is not listed; full world refuses a join; chat filter blocks a bad word in kids mode; music changes when combat starts.
- On failure, CI uploads the Playwright report with traces and screenshots for 7 days.
