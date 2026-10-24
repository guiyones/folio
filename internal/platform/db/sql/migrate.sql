-- Version: 1.01
-- Description: Create clients, keys, accounts and certificates
CREATE TABLE clients (
	id         UUID        NOT NULL,
	name       TEXT        NOT NULL,
	created_at TIMESTAMPTZ NOT NULL,

	PRIMARY KEY (id)
);

CREATE TABLE api_keys (
	id         UUID        NOT NULL,
	client_id  UUID        NOT NULL REFERENCES clients (id),
	hash       BYTEA       NOT NULL,
	prefix     TEXT        NOT NULL,
	created_at TIMESTAMPTZ NOT NULL,
	revoked_at TIMESTAMPTZ,

	PRIMARY KEY (id),
	CONSTRAINT api_keys_hash_key UNIQUE (hash),
	CONSTRAINT api_keys_prefix_key UNIQUE (prefix)
);

CREATE TABLE accounts (
	id             UUID        NOT NULL,
	name           TEXT        NOT NULL,
	cnpj           CHAR(14)    NOT NULL CHECK (cnpj ~ '^[0-9]{14}$'),
	uf             SMALLINT    NOT NULL,
	env            SMALLINT    NOT NULL CHECK (env IN (1, 2)),
	ult_nsu        BIGINT      NOT NULL DEFAULT 0 CHECK (ult_nsu >= 0),
	next_scan_at   TIMESTAMPTZ NOT NULL,
	last_scan_at   TIMESTAMPTZ,
	last_cstat     TEXT        NOT NULL DEFAULT '',
	last_message   TEXT        NOT NULL DEFAULT '',
	paused         BOOLEAN     NOT NULL DEFAULT false,
	auto_ciencia   BOOLEAN     NOT NULL DEFAULT true,
	ciencia_cutoff TIMESTAMPTZ NOT NULL,
	created_at     TIMESTAMPTZ NOT NULL,

	PRIMARY KEY (id),
	CONSTRAINT accounts_cnpj_env_key UNIQUE (cnpj, env)
);

CREATE TABLE client_accounts (
	client_id  UUID NOT NULL REFERENCES clients (id),
	account_id UUID NOT NULL REFERENCES accounts (id),

	PRIMARY KEY (client_id, account_id)
);

CREATE TABLE certificates (
	account_id  UUID        NOT NULL REFERENCES accounts (id),
	holder      TEXT        NOT NULL,
	cnpj        CHAR(14)    NOT NULL,
	not_after   TIMESTAMPTZ NOT NULL,
	key_id      TEXT        NOT NULL,
	pfx         BYTEA       NOT NULL,
	password    BYTEA       NOT NULL,
	uploaded_at TIMESTAMPTZ NOT NULL,

	PRIMARY KEY (account_id)
);
