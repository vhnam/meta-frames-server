-- +goose Up
-- An account shows its money in one currency. Amounts stay whole numbers; only the label changes.
-- Existing accounts keep VND. Once an account holds money evidence, its currency is locked.
ALTER TABLE app_user
  ADD COLUMN currency_code   text    NOT NULL DEFAULT 'VND' CHECK (currency_code IN ('VND', 'USD', 'EUR')),
  ADD COLUMN currency_locked boolean NOT NULL DEFAULT false;

-- +goose StatementBegin
-- True when the account ever held a price: a live or soft-deleted roll or job with a price (zero
-- counts), any lab order, or an audit entry that shows one. The audit trigger names the entity by
-- table name and snapshots the whole row, so a cleared price or a deleted order line still shows.
-- Audit entries take their owner from audit_log.owner_id, never from the actor.
CREATE FUNCTION account_has_money_evidence(owner uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
  SELECT EXISTS (SELECT 1 FROM roll WHERE owner_id = owner AND price IS NOT NULL)
      OR EXISTS (SELECT 1 FROM processing WHERE owner_id = owner AND price IS NOT NULL)
      OR EXISTS (SELECT 1 FROM lab_order WHERE owner_id = owner)
      OR EXISTS (
        SELECT 1 FROM audit_log
        WHERE owner_id = owner
          AND ((entity_type IN ('roll', 'processing')
                AND (before->>'price' IS NOT NULL OR after->>'price' IS NOT NULL))
            OR (entity_type = 'lab_order'
                AND (jsonb_typeof(before) = 'object' OR jsonb_typeof(after) = 'object'))
            OR (entity_type = 'lab_order_item'
                AND (before->>'unit_price' IS NOT NULL OR after->>'unit_price' IS NOT NULL)))
      );
$$;
-- +goose StatementEnd

UPDATE app_user SET currency_locked = true WHERE account_has_money_evidence(id);

-- +goose StatementBegin
-- Same as 00011, plus the lock: a claim that hands over money history locks the claimant.
CREATE OR REPLACE FUNCTION claim_unowned_data(new_owner uuid) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_advisory_xact_lock(hashtext('claim_unowned_data'));
  -- Parents before children, so each composite foreign key finds its parent already moved.
  UPDATE camera     SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE lens       SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE film_stock SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE lab        SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE scanner    SET owner_id = new_owner WHERE owner_id IS NULL AND id = '93d019be-a6d0-4e7a-aaa3-72acbc2f8f1c';
  UPDATE roll       SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE processing SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE frame      SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE scan       SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE audit_log  SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE app_user   SET currency_locked = true WHERE id = new_owner AND account_has_money_evidence(new_owner);
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION claim_unowned_data(new_owner uuid) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_advisory_xact_lock(hashtext('claim_unowned_data'));
  -- Parents before children, so each composite foreign key finds its parent already moved.
  UPDATE camera     SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE lens       SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE film_stock SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE lab        SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE scanner    SET owner_id = new_owner WHERE owner_id IS NULL AND id = '93d019be-a6d0-4e7a-aaa3-72acbc2f8f1c';
  UPDATE roll       SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE processing SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE frame      SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE scan       SET owner_id = new_owner WHERE owner_id IS NULL;
  UPDATE audit_log  SET owner_id = new_owner WHERE owner_id IS NULL;
END;
$$;
-- +goose StatementEnd

DROP FUNCTION account_has_money_evidence(uuid);
ALTER TABLE app_user DROP COLUMN currency_locked, DROP COLUMN currency_code;
