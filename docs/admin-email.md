# Email and password resets for admins

Email is optional. A Talespeak server works without it: admins create accounts or allow self-registration with a name, and when someone forgets their password an admin (or, on a school server, the player's teacher) makes a one-time **reset code** and hands it over. Kids never need an email address, and in edu mode they can never add one.

Turn email on when players and teachers should be able to reset a forgotten password by themselves, with a link that comes by mail.

## What works without email

| Who forgot | Who helps | How |
| --- | --- | --- |
| Any account | An admin | In the lobby: **your account → Reset code for someone else**, type the account name, hand over the code. Or on the server: `talespeak account resetcode NAME` |
| A player of a school (edu mode) world | That world's game master (the teacher) | The same form in the lobby. A game master makes codes only for plain players of their own worlds, never for other game masters or admins |
| An admin locked out | Whoever runs the server | `talespeak account resetcode NAME` or `talespeak account password NAME` on the server ([accounts.md](accounts.md)) |

The player chooses **I have a reset code** on the sign-in page, types the code and a new password, and is signed in. A code looks like `7KQ2M-XR4WD`, works once and for one day, and a new one replaces the last. Using it signs the account out on every other device and ends its open games. The server keeps only a hash of each code.

## How Talespeak sends mail

Talespeak does not run its own mail server. It is an SMTP client (Go's standard `net/smtp`, no extra library) and hands every mail to an SMTP server you choose, which delivers it. Delivering mail directly from a small VPS mostly ends in spam folders, because receiving servers trust only senders with a good reputation and correct DNS records.

The connection to the SMTP server is always encrypted: STARTTLS (port 587) or TLS from the start (port 465). A server that offers neither gets no mail and no password; Talespeak has no setting for plain text.

Mails wait in an outbox in the database and are sent outside any request, so a slow or unreachable mail server never slows down sign-in or the game. A mail that fails is tried again after 1, 2, 4, ... minutes (at most 30 between tries, 8 tries in all). A reset link's mail goes when its link stops working (after an hour, or once used or replaced), so a late mail never brings a dead link, and the code in its text is not kept longer than that. At most 1000 mails wait, and at most 500 of them confirm addresses (which anyone may ask for), so reset links always find room; past that new ones are refused until the outbox drains.

Talespeak sends only two kinds of mail, each naming the account it is for and nothing else (no chat, nothing about other players):

- **Reset your Talespeak password**: a link that works for one hour, and the same code to type.
- **Confirm your email address for Talespeak**: a link that works for one day. Only a confirmed address gets reset links or signs in.

## Choose an SMTP server

| Option | Good for | Notes |
| --- | --- | --- |
| Your school's or company's mail server (Microsoft 365, Google Workspace, own Postfix) | Schools | Ask IT for an SMTP relay account or app password. Often the easiest way to pass school spam filters. |
| A transactional email provider (for example Postmark, Mailgun, Amazon SES, Brevo, SMTP2GO) | Public servers | Every one of them offers plain SMTP. Free tiers usually cover a small server. You verify your domain with them. |
| Your own mail server (Postfix, Stalwart, Mailcow) | People who already run one | You are responsible for reputation, DNS and blocklists. |

Any server that speaks SMTP with STARTTLS or TLS and, if it wants one, a user name and password (AUTH PLAIN) works; Talespeak has no provider-specific code.

## Settings

Put these in `.env` next to `compose.yml`. The password is a secret: keep it only in `.env` or your secret store, never in a protocol file, issue or commit. A wrong value stops the server at startup with a message that names the setting (never the password).

```sh
TALESPEAK_EMAIL_REGISTRATION=true            # mail on: reset links, and email addresses on accounts
TALESPEAK_PUBLIC_URL=https://play.example.org # where players open Talespeak; links in mails start with it
TALESPEAK_SMTP_HOST=smtp.example.org
TALESPEAK_SMTP_PORT=587                      # default: 587 with starttls, 465 with tls
TALESPEAK_SMTP_TLS=starttls                  # starttls (default) | tls
TALESPEAK_SMTP_USER=talespeak@example.org    # leave out with the password for a relay that needs no sign-in
TALESPEAK_SMTP_PASSWORD=$SMTP_PASSWORD       # or an API key, depending on the provider
TALESPEAK_SMTP_FROM="Talespeak <no-reply@example.org>"
```

| Variable | Values | Default | What it does |
| --- | --- | --- | --- |
| `TALESPEAK_EMAIL_REGISTRATION` | `true`, `false` | `false` | Mail on. Needs `TALESPEAK_SMTP_HOST` (and the rest) and `TALESPEAK_PUBLIC_URL` |
| `TALESPEAK_PUBLIC_URL` | `https://host[:port][/path]` | none | The address players open. Links in mails are built from it, never from the request's Host header, so a forged request cannot send a link to another site. `http://` only for `localhost` |
| `TALESPEAK_SMTP_HOST` | a host name | none | The SMTP server. Enough on its own for `mail test` |
| `TALESPEAK_SMTP_PORT` | 1 to 65535 | `587`; `465` with `tls` | |
| `TALESPEAK_SMTP_TLS` | `starttls`, `tls` | `starttls` | `starttls` connects in plain text and switches to TLS before anything else is said (a server that does not offer it is refused); `tls` is encrypted from the first byte. The server's certificate must be valid for the host name |
| `TALESPEAK_SMTP_USER`, `TALESPEAK_SMTP_PASSWORD` | | none | Both or neither |
| `TALESPEAK_SMTP_FROM` | an address, optionally with a name | none | The `From` of every mail; on a domain your provider sends for |

- Port 25 is blocked for outgoing mail by most VPS hosts; you do not need it.
- Restart after changing settings: `docker compose up -d`.

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

The command reads the `TALESPEAK_SMTP_*` settings (mail need not be on yet), sends one test mail at once, without the outbox, and prints that the server accepted it or what it answered. Common errors:

| Error | Likely cause |
| --- | --- |
| `connect to ...: connection refused` or a timeout | Wrong host or port, or the VPS host blocks that port |
| `the server does not offer STARTTLS` | Use `TALESPEAK_SMTP_TLS=tls` and port 465, or another server |
| `certificate` errors | The host name does not match the server's certificate; use the name your provider documents |
| `authentication failed` | Wrong user or password, or the provider wants an API key or app password |
| `sender not accepted` | The `From` domain is not verified with the provider |
| Mail arrives in spam | SPF, DKIM or DMARC missing; check with your provider's domain check |

The server log says when a mail is sent, retried or given up, by its number in the outbox; it never names the address (the SMTP server's answer has it replaced by `[address]`).

When you log a test in `protocol/`, write the variables (`$TALESPEAK_SMTP_HOST`), never their values.

## What players and teachers see

- **Registering** (public mode, self-registration): an optional email field. A confirmation mail goes to the address; the account works without confirming it.
- **Your account** in the lobby: add, change or remove the address (a new one is kept only once its link is opened), change the password, and for admins and teachers make reset codes.
- **Forgot password?** on the sign-in page: type a name or a confirmed address. The answer is the same whether or not the account exists or has an address, so nobody learns either. Each address gets at most three mails at once and one every 20 minutes after, whoever asks, so nobody can fill someone's mailbox; each network address may ask 10 times at once, then once a minute.
- A confirmed address also works as the name when signing in.

## Kids and privacy

- In edu mode kids register without email, even when mail is on, and cannot add an address later; only admins and game masters (teachers) can. A teacher who is no longer a game master can no longer confirm a new address, and their old one gets no reset links.
- Teachers' and admins' email addresses are used only for sign-in, confirmation and password reset, never for newsletters.
- Mails contain no chat content and no information about kids.
