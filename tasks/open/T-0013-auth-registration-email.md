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

- [ ] Local username and password accounts, argon2id hashes, session cookies (HttpOnly, Secure, SameSite=Strict)
- [ ] Auth provider interface; local provider only for now
- [ ] Settings: registration off / self / SSO only, optional class code, email registration on or off
- [ ] Registering never grants access: edu kids wait for a GM invite, teachers for an admin to make them GM
- [ ] One-time reset codes created by teachers or admins (works without email)
- [ ] Email via SMTP client (wneessen/go-mail), STARTTLS or TLS only, outbox table with retries
- [ ] `talespeak mail test <address>` command
- [ ] Login and registration rate limits
- [ ] Tests: each registration mode, edu safety rules, fake SMTP server

## Notes

Later tasks: OIDC (coreos/go-oidc; Google Workspace, Microsoft Entra ID, Keycloak) and SAML (crewjam/saml). Admin guide: docs/admin-email.md. Section "Sign-in, registration and email" in the design doc. Design: Talespeak Engine Design doc, https://claude.ai/code/artifact/15bc0469-681c-4afa-b7b9-5d227560ebc6
