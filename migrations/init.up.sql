-- ================================
-- Enums
-- ================================
CREATE TYPE message_type AS ENUM (
    'PARSE',
    'ASSESS',
    'NOTIFY'
    );

CREATE TYPE message_status AS ENUM (
    'READY',
    'RETRY',
    'PUBLISHED'
    );

-- ================================
-- Tables
-- ================================
CREATE TABLE state
(
    id         BIGSERIAL PRIMARY KEY,
    data       TEXT     NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP
);

CREATE TABLE patient
(
    id            BIGSERIAL PRIMARY KEY,
    name          VARCHAR(200) NOT NULL,
    ssn           VARCHAR(11)  NOT NULL UNIQUE,
    current_state BIGINT REFERENCES state (id),
    created_at    TIMESTAMP    NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMP    NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMP
);

CREATE TABLE record
(
    id              BIGSERIAL PRIMARY KEY,
    cloud_reference TEXT      NOT NULL,
    metadata        JSONB,
    created_at      TIMESTAMP NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMP
);

CREATE TABLE patient_record_map
(
    user_id   BIGINT REFERENCES patient (id) ON DELETE CASCADE,
    record_id BIGINT REFERENCES record (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, record_id)
);

CREATE TABLE patient_state_map
(
    user_id  BIGINT REFERENCES patient (id) ON DELETE CASCADE,
    state_id BIGINT REFERENCES state (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, state_id)
);

CREATE TABLE message_queue
(
    id     BIGSERIAL PRIMARY KEY,
    type   message_type   NOT NULL,
    data   JSONB,
    status message_status NOT NULL DEFAULT 'READY'
);