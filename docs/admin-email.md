# Email setup for admins

Email is optional. A Talespeak server works without it: admins create accounts or allow self-registration with a username, and teachers or admins reset passwords with one-time codes. Kids never need an email address.

Turn email on when you want sign-up with an email address, email verification, or password reset by email.

## How Talespeak sends mail

Talespeak does not run its own mail server. It is an SMTP client and hands every mail to an SMTP server you choose, which then delivers it. Delivering mail directly from a small VPS mostly ends in spam folders, because receiving servers trust only senders with a good reputation and correct DNS records.

Mails wait in an outbox in the database and are retried, so a slow or unreachable mail server never blocks the game.

## Choose an SMTP server

| Option | Good for | Notes |
| --- | --- | --- |
| Your school's or company's mail server (Microsoft 365, Google Workspace, own Postfix) | Schools | Ask IT for an SMTP relay account or app password. Often the easiest way to pass school spam filters. |
| A transactional email provider (for example Postmark, Mailgun, Amazon SES, Brevo, SMTP2GO) | Public servers | Every one of them offers plain SMTP. Free tiers usually cover a small server. You verify your domain with them. |
| Your own mail server (Postfix, Stalwart, Mailcow) | People who already run one | You are responsible for reputation, DNS and blocklists. |

Any server that speaks SMTP with authentication works; Talespeak has no provider-specific code.

## Settings

Put these in `.env` next to `compose.yml`. The password is a secret: keep it only in `.env` or your secret store, never in a protocol file, issue or commit.

```sh
TALESPEAK_EMAIL_REGISTRATION=true          # offer sign-up and reset by email
TALESPEAK_SMTP_HOST=smtp.example.org
TALESPEAK_SMTP_PORT=587                    # 587 = STARTTLS, 465 = TLS from the start
TALESPEAK_SMTP_TLS=starttls                # starttls | tls
TALESPEAK_SMTP_USER=talespeak@example.org
TALESPEAK_SMTP_PASSWORD=change-me          # or an API key, depending on the provider
TALESPEAK_SMTP_FROM="Talespeak <no-reply@example.org>"
```

- Use port 587 with STARTTLS or port 465 with TLS. Talespeak refuses to send passwords over an unencrypted connection.
- Port 25 is blocked for outgoing mail by most VPS hosts; you do not need it.
- The `From` address must be on a domain your SMTP provider is allowed to send for.

Restart after changing settings:

```sh
docker compose up -d
```

## DNS records for your sending domain

Your provider shows the exact values; these are the three records it will ask for.

| Record | Type | Example | Why |
| --- | --- | --- | --- |
| SPF | TXT on `example.org` | `v=spf1 include:spf.provider.example ~all` | Lists the servers allowed to send for your domain |
| DKIM | TXT (or CNAME) on `selector._domainkey.example.org` | from your provider | Signs each mail so receivers can verify it |
| DMARC | TXT on `_dmarc.example.org` | `v=DMARC1; p=none; rua=mailto:dmarc@example.org` | Tells receivers what to do with unsigned mail; move to `p=quarantine` once reports look clean |

A domain can have only one SPF record. If you already send mail for the domain, add the provider's `include:` to the existing record instead of creating a second one.

## Test it

```sh
docker compose exec talespeak /talespeak mail test you@example.org
```

The command sends one test mail and prints the SMTP server's answer. Common errors:

| Error | Likely cause |
| --- | --- |
| `connection refused` or timeout | Wrong host or port, or the VPS host blocks that port |
| `authentication failed` | Wrong user or password, or the provider wants an API key or app password |
| `sender not allowed` | The `From` domain is not verified with the provider |
| Mail arrives in spam | SPF, DKIM or DMARC missing; check with your provider's domain check |

When you log a test in `protocol/`, write the variables (`$TALESPEAK_SMTP_HOST`), never their values.

## Kids and privacy

- In edu mode kids register without email, even when email registration is on.
- Teachers' and admins' email addresses are used only for sign-in, verification and password reset, never for newsletters.
- Mails contain no chat content and no information about kids.
