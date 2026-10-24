-- After the payer acknowledges a claim, Payahub asks it again from time to
-- time until the claim is paid or rejected, so the clinic sees the outcome.
ALTER TABLE claims ADD COLUMN IF NOT EXISTS status_check_at timestamptz;
ALTER TABLE claims ADD COLUMN IF NOT EXISTS status_checks integer NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS claims_status_check_idx ON claims (status_check_at) WHERE status_check_at IS NOT NULL;
