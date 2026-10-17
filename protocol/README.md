# Protocol

Every test run, manual check, benchmark, deploy or investigation gets an entry here: the exact commands, trimmed output and the result. Anyone can repeat it later.

## Files

`protocol/YYYY-MM-DD-<slug>.md`, one per session of work, linked from the task it belongs to. Copy `_template.md`.

## Rules

1. **No secrets, ever.** Tokens, passwords, keys, cookies and private hostnames go into environment variables, and only the variable name is written down:

   ```sh
   curl -H "Authorization: Bearer $TALESPEAK_TOKEN" "$TALESPEAK_URL/api/v1/me"
   ```

   CI runs a secret scan on every push and fails if anything slips through.
2. **Commands verbatim**, in fenced `sh` blocks, in the order they were run, including the ones that failed.
3. **Output trimmed** to what proves the point. Mark cuts with `…`.
4. **Result in one line** at the top: passed, failed, or what was learned.
5. **Environment stated:** which instance (local, dev, a user's server), version or commit, and the client (browser and version, curl, a test script).

## Kinds of entries

| Kind | Example |
| --- | --- |
| API black-box test | `curl` and `websocat` calls against the dev instance |
| UI test | Playwright script run, or manual steps with the browser used |
| Benchmark | Whisp latency on a 1 vCPU box |
| Deploy | Commands used to set up or update a server |
| Research | Commands used to inspect another project |
