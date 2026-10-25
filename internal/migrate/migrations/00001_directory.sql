-- +goose Up
-- Mailbox users, distribution lists, and membership.
-- Postfix alias lookup:
--   SELECT destination FROM virtual_alias_maps WHERE source = lower('%s')
-- Postfix mailbox lookup:
--   SELECT address FROM users WHERE address = lower('%s')

CREATE TABLE users (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    address text NOT NULL,
    display_name text NOT NULL DEFAULT '',
    password_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_address_unique UNIQUE (address),
    CONSTRAINT users_address_format CHECK (
        address = lower(address)
        AND position('@' IN address) > 1
        AND length(address) <= 254
    ),
    CONSTRAINT users_password_present CHECK (length(password_hash) > 0)
);

CREATE TABLE distribution_lists (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    address text NOT NULL,
    display_name text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT distribution_lists_address_unique UNIQUE (address),
    CONSTRAINT distribution_lists_address_format CHECK (
        address = lower(address)
        AND position('@' IN address) > 1
        AND length(address) <= 254
    )
);

CREATE TABLE distribution_list_members (
    list_id bigint NOT NULL REFERENCES distribution_lists (id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (list_id, user_id)
);

CREATE INDEX distribution_list_members_user_idx
    ON distribution_list_members (user_id);

-- +goose StatementBegin
CREATE FUNCTION set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER users_set_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER distribution_lists_set_updated_at
    BEFORE UPDATE ON distribution_lists
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- A mailbox address and a list address must not be the same, or Postfix
-- would recurse while expanding aliases.
-- +goose StatementBegin
CREATE FUNCTION reject_shared_mail_address() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'users' THEN
        IF EXISTS (SELECT 1 FROM distribution_lists WHERE address = NEW.address) THEN
            RAISE EXCEPTION 'address already used by a distribution list'
                USING ERRCODE = 'unique_violation';
        END IF;
    ELSE
        IF EXISTS (SELECT 1 FROM users WHERE address = NEW.address) THEN
            RAISE EXCEPTION 'address already used by a user'
                USING ERRCODE = 'unique_violation';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER users_reject_shared_address
    BEFORE INSERT OR UPDATE OF address ON users
    FOR EACH ROW EXECUTE FUNCTION reject_shared_mail_address();

CREATE TRIGGER distribution_lists_reject_shared_address
    BEFORE INSERT OR UPDATE OF address ON distribution_lists
    FOR EACH ROW EXECUTE FUNCTION reject_shared_mail_address();

CREATE VIEW virtual_alias_maps AS
SELECT l.address AS source, u.address AS destination
FROM distribution_lists AS l
JOIN distribution_list_members AS m ON m.list_id = l.id
JOIN users AS u ON u.id = m.user_id;

-- +goose Down
DROP VIEW IF EXISTS virtual_alias_maps;
DROP TABLE IF EXISTS distribution_list_members;
DROP TABLE IF EXISTS distribution_lists;
DROP TABLE IF EXISTS users;
DROP FUNCTION IF EXISTS reject_shared_mail_address();
DROP FUNCTION IF EXISTS set_updated_at();
