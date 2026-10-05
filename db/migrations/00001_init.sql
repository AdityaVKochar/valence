-- +goose Up
CREATE TABLE schema_info (
    key        text PRIMARY KEY,
    value      text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO schema_info (key, value) VALUES ('project', 'valence');

-- +goose StatementBegin
CREATE FUNCTION set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TYPE user_role AS ENUM ('contestant', 'setter', 'admin');

CREATE TABLE users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    handle        text NOT NULL CHECK (handle ~ '^[A-Za-z0-9_-]{3,32}$'),
    display_name  text NOT NULL DEFAULT '' CHECK (length(display_name) <= 64),
    email         text CHECK (length(email) <= 254),
    avatar_url    text,
    role          user_role NOT NULL DEFAULT 'contestant',
    disabled_at   timestamptz,
    last_login_at timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX users_handle_key ON users (lower(handle));
CREATE INDEX users_email_idx ON users (lower(email)) WHERE email IS NOT NULL;

CREATE TRIGGER users_set_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE identities (
    provider     text NOT NULL CHECK (provider IN ('github', 'google', 'dev')),
    subject      text NOT NULL,
    user_id      bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    email        text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, subject)
);

CREATE INDEX identities_user_id_idx ON identities (user_id);

CREATE TABLE sessions (
    id_hash    bytea PRIMARY KEY CHECK (length(id_hash) = 32),
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    user_agent text NOT NULL DEFAULT '',
    ip         inet
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

CREATE TYPE problem_visibility AS ENUM ('private', 'public');

CREATE TABLE problems (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug             text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,63}$'),
    title            text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    statement_md     text NOT NULL DEFAULT '' CHECK (octet_length(statement_md) <= 262144),
    time_limit_ms    integer NOT NULL DEFAULT 1000 CHECK (time_limit_ms BETWEEN 100 AND 20000),
    memory_limit_kib integer NOT NULL DEFAULT 262144 CHECK (memory_limit_kib BETWEEN 16384 AND 2097152),
    checker          text NOT NULL DEFAULT 'tokens'
                     CHECK (checker ~ '^(exact|tokens|float:[0-9]+(\.[0-9]+)?([eE][-+]?[0-9]+)?)$'),
    visibility       problem_visibility NOT NULL DEFAULT 'private',
    revision         integer NOT NULL DEFAULT 1 CHECK (revision >= 1),
    created_by       bigint REFERENCES users (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX problems_public_idx ON problems (id) WHERE visibility = 'public';

CREATE TRIGGER problems_set_updated_at BEFORE UPDATE ON problems
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE tests (
    problem_id  bigint NOT NULL REFERENCES problems (id) ON DELETE CASCADE,
    ordinal     integer NOT NULL CHECK (ordinal >= 1),
    input_hash  text NOT NULL CHECK (input_hash ~ '^[0-9a-f]{64}$'),
    input_size  bigint NOT NULL CHECK (input_size >= 0),
    output_hash text NOT NULL CHECK (output_hash ~ '^[0-9a-f]{64}$'),
    output_size bigint NOT NULL CHECK (output_size >= 0),
    is_sample   boolean NOT NULL DEFAULT false,
    PRIMARY KEY (problem_id, ordinal)
);

CREATE TYPE submission_status AS ENUM (
    'queued', 'leased', 'compiling', 'running', 'checking', 'finalized', 'retryable_error'
);

CREATE TYPE verdict AS ENUM ('AC', 'WA', 'TLE', 'MLE', 'RE', 'OLE', 'CE', 'IE');

CREATE TABLE submissions (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id          bigint NOT NULL REFERENCES users (id),
    problem_id       bigint NOT NULL REFERENCES problems (id),
    problem_revision integer NOT NULL,
    language         text NOT NULL CHECK (language ~ '^[a-z0-9_.+-]{1,32}$'),
    source           text NOT NULL CHECK (octet_length(source) <= 262144),
    attempt          integer NOT NULL DEFAULT 1 CHECK (attempt >= 1),
    status           submission_status NOT NULL DEFAULT 'queued',
    verdict          verdict,
    time_ms          integer,
    memory_kib       integer,
    current_test     integer,
    created_at       timestamptz NOT NULL DEFAULT now(),
    judged_at        timestamptz,
    CONSTRAINT submissions_verdict_when_final CHECK ((status = 'finalized') = (verdict IS NOT NULL))
);

CREATE INDEX submissions_user_idx ON submissions (user_id, id DESC);
CREATE INDEX submissions_user_problem_idx ON submissions (user_id, problem_id, id DESC);
CREATE INDEX submissions_problem_idx ON submissions (problem_id, id DESC);
CREATE INDEX submissions_unfinished_idx ON submissions (created_at) WHERE status <> 'finalized';

CREATE TABLE submission_attempts (
    submission_id  bigint NOT NULL REFERENCES submissions (id) ON DELETE CASCADE,
    attempt        integer NOT NULL CHECK (attempt >= 1),
    status         submission_status NOT NULL DEFAULT 'queued',
    verdict        verdict,
    time_ms        integer,
    memory_kib     integer,
    compile_output text CHECK (octet_length(compile_output) <= 65536),
    provider       text,
    worker         text,
    infra_retries  integer NOT NULL DEFAULT 0,
    last_error     text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    started_at     timestamptz,
    finished_at    timestamptz,
    PRIMARY KEY (submission_id, attempt),
    CONSTRAINT attempts_verdict_when_final CHECK ((status = 'finalized') = (verdict IS NOT NULL))
);

CREATE TABLE test_results (
    submission_id bigint NOT NULL,
    attempt       integer NOT NULL,
    ordinal       integer NOT NULL CHECK (ordinal >= 1),
    verdict       verdict NOT NULL,
    time_ms       integer NOT NULL CHECK (time_ms >= 0),
    memory_kib    integer NOT NULL CHECK (memory_kib >= 0),
    PRIMARY KEY (submission_id, attempt, ordinal),
    FOREIGN KEY (submission_id, attempt)
        REFERENCES submission_attempts (submission_id, attempt) ON DELETE CASCADE
);

CREATE TABLE jobs (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind         text NOT NULL,
    priority     smallint NOT NULL DEFAULT 0,
    payload      bytea NOT NULL,
    dedupe_key   text UNIQUE,
    available_at timestamptz NOT NULL DEFAULT now(),
    lease_token  uuid,
    leased_until timestamptz,
    attempts     integer NOT NULL DEFAULT 0,
    last_error   text,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX jobs_lease_idx ON jobs (kind, priority DESC, id);

-- +goose StatementBegin
CREATE FUNCTION jobs_notify() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('jobs', NEW.kind);
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER jobs_notify AFTER INSERT ON jobs
    FOR EACH ROW EXECUTE FUNCTION jobs_notify();

-- +goose Down
DROP TRIGGER jobs_notify ON jobs;
DROP FUNCTION jobs_notify();
DROP TABLE jobs;
DROP TABLE test_results;
DROP TABLE submission_attempts;
DROP TABLE submissions;
DROP TYPE verdict;
DROP TYPE submission_status;
DROP TABLE tests;
DROP TABLE problems;
DROP TYPE problem_visibility;
DROP TABLE sessions;
DROP TABLE identities;
DROP TABLE users;
DROP TYPE user_role;
DROP FUNCTION set_updated_at();
DROP TABLE schema_info;
