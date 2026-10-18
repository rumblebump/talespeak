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
- [ ] Auth provider interface; local provider only for now
- [ ] Settings: registration off / self / SSO only, optional class code, email registration on or off
- [ ] Registering never grants access: edu kids wait for a GM invite, teachers for an admin to make them GM
- [ ] One-time reset codes created by teachers or admins (works without email)
- [ ] Email via SMTP client (wneessen/go-mail), STARTTLS or TLS only, outbox table with retries
- [ ] `talespeak mail test <address>` command
- [x] Login and registration rate limits
- [ ] Tests: each registration mode, edu safety rules, fake SMTP server

## Notes

Done in T-0034 (2026-10-18): local accounts, sessions, rate limits, `TALESPEAK_REGISTRATION` self or off, edu mode always invite only, admin commands on the command line ([docs/accounts.md](../../docs/accounts.md)). Still open: the provider interface, SSO-only and class-code settings, reset codes, email; and from the T-0034 security review: a password change, sign-out or demotion does not yet end a game that is already open (the WebSocket checks the sign-in only when it connects), so the connection should check it again from time to time.

Later tasks: OIDC (coreos/go-oidc; Google Workspace, Microsoft Entra ID, Keycloak) and SAML (crewjam/saml). Admin guide: docs/admin-email.md. Section "Sign-in, registration and email" in the design doc. Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
