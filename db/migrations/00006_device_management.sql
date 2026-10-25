-- +goose Up
-- A device is provisioned before it has a key: it holds no usable key_hash until
-- its firmware redeems a pairing code. The UNIQUE constraint on key_hash still
-- holds because PostgreSQL treats NULLs as distinct, so many unpaired devices can
-- coexist and a NULL hash never matches a presented key.
ALTER TABLE devices ALTER COLUMN key_hash DROP NOT NULL;

ALTER TABLE devices
    ADD COLUMN paired_at timestamptz,
    ADD COLUMN firmware_version text,
    ADD COLUMN battery_percent smallint CHECK (battery_percent BETWEEN 0 AND 100),
    ADD COLUMN pending_queue_depth integer CHECK (pending_queue_depth >= 0),
    ADD COLUMN free_storage_bytes bigint CHECK (free_storage_bytes >= 0),
    ADD COLUMN status_reported_at timestamptz;

CREATE TABLE device_pairing_codes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL,
    device_id uuid NOT NULL,
    code_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_by_user_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (institution_id, device_id) REFERENCES devices(institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, created_by_user_id) REFERENCES users(institution_id, id) ON DELETE RESTRICT
);

-- At most one outstanding (unconsumed) pairing code per device.
CREATE UNIQUE INDEX device_pairing_codes_one_active
    ON device_pairing_codes(institution_id, device_id)
    WHERE consumed_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS device_pairing_codes_one_active;
DROP TABLE IF EXISTS device_pairing_codes;

ALTER TABLE devices
    DROP COLUMN status_reported_at,
    DROP COLUMN free_storage_bytes,
    DROP COLUMN pending_queue_depth,
    DROP COLUMN battery_percent,
    DROP COLUMN firmware_version,
    DROP COLUMN paired_at;

-- Restoring NOT NULL requires every device to hold a key again. Unpaired devices
-- never received one, so this dev-only rollback removes them first.
DELETE FROM devices WHERE key_hash IS NULL;
ALTER TABLE devices ALTER COLUMN key_hash SET NOT NULL;
