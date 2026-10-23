-- What the de-identifier removed before each model call (kinds and counts,
-- never the values), so an audit can show nothing identifying was sent.
ALTER TABLE ai_runs ADD COLUMN IF NOT EXISTS privacy jsonb NOT NULL DEFAULT '{}';
