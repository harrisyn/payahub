# Loading Serenity's payers

Serenity (core-api-v2) keeps a payer per organisation in `payers`, the
platform that reaches it through `payer_plugins` → `plugins`, and the payer's
reference on that platform in `payers.external_reference_id` (Medserve's
insurer code, SmartClaims' HICD, XCeli's insurer API URL, RX's insurance
company code). Run this read-only query against Serenity's database and save
the result as CSV:

```sql
SELECT DISTINCT
       p.name,
       pl.name                          AS platform,
       nullif(trim(p.external_reference_id), '') AS external_reference_id,
       coalesce(p.country_code, 'GH')  AS country,
       p.is_active
FROM payers p
JOIN payer_plugins pp ON pp.payer_id = p.uuid
JOIN plugins pl       ON pl.id = pp.plugin_id
ORDER BY p.name;
```

```sh
psql "$SERENITY_DATABASE_URL" -c "\copy (<the query>) TO 'serenity-payers.csv' WITH CSV HEADER"
```

Then, as an operator, open **Catalogue → Import payers** in the console (or
`POST /v1/operator/payers/import`), check the file first, and import.

What the import does with each row:

- **add**: a payer not yet in the catalogue, on the platform named, with its reference as that
  platform's payer setting. An inactive Serenity payer is added withdrawn.
- **fill**: the payer is in the catalogue (same name) on the same platform but a setting is empty; the
  reference fills it.
- **same**: nothing to change (Serenity holds one copy per organisation, so repeats are expected).
- **conflict**: the file disagrees with the catalogue or with an earlier row (another platform, another
  reference). Nothing is changed; decide in the payer's page.
- **skip**: the platform is not one Payahub connects to (manual, portal, email), or the name gives no
  usable id.
