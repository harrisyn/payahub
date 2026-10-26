-- Finding a claim's possible duplicates: the same organisation, payer,
-- member and visit date.
CREATE INDEX IF NOT EXISTS claims_duplicate_idx ON claims (
    organization_id, payer_id, (upper(trim(body->'member'->>'member_number'))), ((body->'encounter'->>'start_date'))
);
