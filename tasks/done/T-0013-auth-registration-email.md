---
id: T-0013
title: Sign-in, registration settings and email
type: feature
priority: high
created: 2026-10-17
depends: [T-0011]
---

## Goal

Local accounts work for the prototype and public test, admins control who may register, optional email covers verification and password reset, and the code is ready for OIDC and SAML later.

## Acceptance

- [x] Local username and password accounts, argon2id hashes, session cookies (HttpOnly, Secure, SameSite=Strict)
- [x] Auth provider interface; local provider only for now
- [x] Settings: registration off / self / SSO only, optional class code, email registration on or off
- [x] Registering never grants access: edu kids wait for a GM invite, teachers for an admin to make them GM
- [x] One-time reset codes created by teachers or admins (works without email)
- [x] Email via SMTP client (Go's net/smtp, design call 15: no new dependency), STARTTLS or TLS only, outbox table with retries
- [x] `talespeak mail test <address>` command
- [x] Login and registration rate limits
- [x] Tests: each registration mode, edu safety rules, fake SMTP server
- [x] From the T-0034 review: a sign-out, password change or reset ends the account's open games at once, and every open game checks its sign-in again every minute

## Notes

Done in T-0034 (2026-10-18): local accounts, sessions, rate limits, `TALESPEAK_REGISTRATION` self or off, edu mode always invite only, admin commands on the command line ([docs/accounts.md](../../docs/accounts.md)). Left open then (done in T-0013 below): the provider interface, SSO-only and class-code settings, reset codes, email; and from the T-0034 security review: a password change, sign-out or demotion does not yet end a game that is already open (the WebSocket checks the sign-in only when it connects), so the connection should check it again from time to time.

Later tasks: OIDC (coreos/go-oidc; Google Workspace, Microsoft Entra ID, Keycloak) and SAML (crewjam/saml). Admin guide: docs/admin-email.md. Section "Sign-in, registration and email" in the design doc. Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6

Done in T-0013 (2026-10-27, design call 15):

- `account.Provider` (`internal/account/provider.go`) with `Local` as the only provider; a name with `@` signs in by confirmed email. OIDC/SAML will add start and callback routes and end in `startSession`.
- `TALESPEAK_REGISTRATION=sso` (works like `off` until an SSO provider exists), `TALESPEAK_CLASS_CODE` (only with `self`; never grants a world), `TALESPEAK_EMAIL_REGISTRATION`, `TALESPEAK_PUBLIC_URL`, `TALESPEAK_SMTP_*`, `TALESPEAK_SIGNIN_RECHECK`.
- Reset codes: `POST /api/resetcode` (admins for anyone; in edu mode GMs only for plain players of their worlds, never GMs or admins), `talespeak account resetcode NAME`, `POST /api/reset`; mailed links via `POST /api/forgot` (one answer for every name, rate limited per address, account and recipient). `POST /api/password` changes one's own password. Migration 0008: `accounts.email`/`email_key`, `account_tokens` (hashes only), `mail_outbox` (a mail goes with its token).
- `internal/mail`: `Send` with net/smtp (STARTTLS or TLS, never plain), `Outbox` with backoff retries, addresses never logged; `internal/mail/mailtest` is the fake SMTP server the tests use.
- Edu: kids never give an address (registration, `/api/email`, `/api/verify` refuse); only admins and GMs do.
- Ending games: `account.Config.SignOut` -> `ws.Server.SignOut` closes the connections of a sign-in (logout) or of the account (reset, password change) with close code 4002; `ws.WithRecheck` re-runs the sign-in check every minute and catches command-line changes, expiry and lost membership.
- Client: `web/static/password.js` (forgot password, reset with a code or `#reset=` link, `#verify=` links, change password, email address, reset codes for others; class code and email at registration).

From the final security review (2026-10-28), fixed: a `#verify=` link waits for a click on **confirm the address** (a mail scanner opening it confirmed an address for whoever typed it); confirmation mails take at most half the outbox (`store.MaxVerifyOutbox`), so reset links still go out; a sign-out between the upgrade's sign-in check and the connection being watched no longer leaves the connection open until the next recheck (`ws.watch` checks once more).

Also fixed then: a reset code a GM made is checked again when it is used (`codeStillAllowed`, `store.AccountByID`): once the player became a GM or an admin, or left the maker's worlds, it is refused.

Still open: SSO providers (OIDC, SAML: later tasks); a route to delete an account (none exists yet; the recheck would end its games within a minute); reset codes made by GMs are logged with slog but not in a world's audit log (they belong to no one world); an email change does not notify the old address.

