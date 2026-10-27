-- Email addresses, one-time codes and the mail outbox (T-0013).

-- An account's confirmed email address, as typed; email_key is it in lower
-- case and unique when set. Unconfirmed addresses wait in account_tokens.
ALTER TABLE accounts ADD COLUMN email TEXT;
ALTER TABLE accounts ADD COLUMN email_key TEXT;
CREATE UNIQUE INDEX accounts_email ON accounts (email_key) WHERE email_key IS NOT NULL;

-- One-time codes: a reset code an admin or game master made ('code'), a
-- reset link sent by mail ('reset'), a link that confirms an address
-- ('verify'). Only a hash of each code is stored.
CREATE TABLE account_tokens (
    token_hash BLOB PRIMARY KEY,
    account_id INTEGER NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('code', 'reset', 'verify')),
    email      TEXT NOT NULL DEFAULT '', -- 'verify': the address it confirms
    created_by INTEGER REFERENCES accounts (id) ON DELETE SET NULL,
    expires_at INTEGER NOT NULL -- Unix seconds
) STRICT, WITHOUT ROWID;
CREATE INDEX account_tokens_account ON account_tokens (account_id, kind);
CREATE INDEX account_tokens_expires ON account_tokens (expires_at);

-- Mails waiting to be sent. A mail with a code goes when its code does
-- (used, replaced or expired), so a link never arrives after it stopped
-- working, and the code in its text never outlives it.
CREATE TABLE mail_outbox (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    token_hash BLOB REFERENCES account_tokens (token_hash) ON DELETE CASCADE,
    to_addr    TEXT NOT NULL,
    subject    TEXT NOT NULL,
    body       TEXT NOT NULL,
    attempts   INTEGER NOT NULL DEFAULT 0,
    next_at    INTEGER NOT NULL, -- Unix seconds: when to try next
    expires_at INTEGER NOT NULL  -- Unix seconds: dropped unsent after it
) STRICT;
CREATE INDEX mail_outbox_next ON mail_outbox (next_at);
CREATE INDEX mail_outbox_expires ON mail_outbox (expires_at);
CREATE INDEX mail_outbox_token ON mail_outbox (token_hash);
