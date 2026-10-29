-- Story 4.1: the policy store. Times are Unix epoch milliseconds. No message
-- content lives here except a report's last_messages (AD-10), deleted once
-- actioned.

CREATE TABLE account (
	account_key TEXT PRIMARY KEY NOT NULL,
	created_ms  INTEGER NOT NULL
);

CREATE TABLE profile (
	account_key TEXT PRIMARY KEY NOT NULL,
	pseudonym   TEXT NOT NULL,
	blurb       TEXT NOT NULL
);

CREATE TABLE block (
	blocker_key TEXT NOT NULL,
	blocked_key TEXT NOT NULL,
	created_ms  INTEGER NOT NULL,
	PRIMARY KEY (blocker_key, blocked_key)
);

-- One row per pair, stored in canonical order (key_a < key_b).
CREATE TABLE cooldown (
	key_a      TEXT NOT NULL,
	key_b      TEXT NOT NULL,
	expires_ms INTEGER NOT NULL,
	PRIMARY KEY (key_a, key_b),
	CHECK (key_a < key_b)
);

CREATE TABLE ban (
	account_key TEXT PRIMARY KEY NOT NULL,
	created_ms  INTEGER NOT NULL,
	note        TEXT NOT NULL DEFAULT ''
);

CREATE TABLE report (
	id            TEXT PRIMARY KEY NOT NULL,
	reporter_key  TEXT NOT NULL,
	reported_key  TEXT NOT NULL,
	created_ms    INTEGER NOT NULL,
	last_messages TEXT NOT NULL
);
