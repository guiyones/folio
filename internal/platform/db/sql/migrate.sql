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

-- Version: 1.02
-- Description: Create documents, notes and events
CREATE SEQUENCE note_seq;

CREATE TABLE dfe_docs (
	id          UUID        NOT NULL,
	account_id  UUID        NOT NULL REFERENCES accounts (id),
	nsu         BIGINT      NOT NULL,
	schema      TEXT        NOT NULL,
	xml         BYTEA       NOT NULL,
	received_at TIMESTAMPTZ NOT NULL,

	PRIMARY KEY (id),
	CONSTRAINT dfe_docs_nsu_key UNIQUE (account_id, nsu)
);

CREATE TABLE notes (
	id               UUID        NOT NULL,
	account_id       UUID        NOT NULL REFERENCES accounts (id),
	key              CHAR(44)    NOT NULL,
	emit_cnpj        TEXT        NOT NULL DEFAULT '',
	emit_name        TEXT        NOT NULL DEFAULT '',
	total_cents      BIGINT      NOT NULL DEFAULT 0,
	issued_at        TIMESTAMPTZ,
	status           TEXT        NOT NULL CHECK (status IN ('resumo', 'completa', 'cancelada')),
	summary_doc_id   UUID        REFERENCES dfe_docs (id),
	full_doc_id      UUID        REFERENCES dfe_docs (id),
	ciencia          TEXT        NOT NULL DEFAULT 'pendente' CHECK (ciencia IN ('pendente', 'feita', 'falhou', 'nao_aplica')),
	ciencia_attempts INT         NOT NULL DEFAULT 0,
	ciencia_next_at  TIMESTAMPTZ,
	seq              BIGINT      NOT NULL,

	PRIMARY KEY (id),
	CONSTRAINT notes_key_key UNIQUE (account_id, key)
);

CREATE INDEX notes_account_seq ON notes (account_id, seq);

CREATE TABLE note_events (
	id          UUID        NOT NULL,
	note_id     UUID        NOT NULL REFERENCES notes (id),
	type        TEXT        NOT NULL,
	seq         INT         NOT NULL,
	description TEXT        NOT NULL DEFAULT '',
	at          TIMESTAMPTZ NOT NULL,
	origin      TEXT        NOT NULL CHECK (origin IN ('sefaz', 'folio')),
	cstat       TEXT        NOT NULL DEFAULT '',
	doc_id      UUID        REFERENCES dfe_docs (id),
	xml         BYTEA,

	PRIMARY KEY (id),
	CONSTRAINT note_events_key UNIQUE (note_id, type, seq, origin)
);
