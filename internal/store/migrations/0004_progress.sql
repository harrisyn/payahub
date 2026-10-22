-- What the payer holds of a claim sent line by line (engine.Progress),
-- written after every payer step so a retry resumes instead of repeating.
ALTER TABLE claims ADD COLUMN IF NOT EXISTS submission_progress jsonb;
