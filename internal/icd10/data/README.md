`icd10cm.tsv.gz` is the ICD-10-CM code list (code without its dot, 1 when the
code is billable at that level, title), from the ICD-10-CM 2026 release of
1 April 2026 by the U.S. National Center for Health Statistics and CMS, which
is in the public domain. Codes that take a seventh character are written out
with each one. Rebuild it from a new release's tabular list (XML) and list of
codes with `go run ./internal/icd10/gen` (see its doc comment).

Ghana's payers use WHO ICD-10, which shares its categories with ICD-10-CM
except a few; `who.tsv` adds the WHO-only codes in common use. A WHO subcode
missing here is accepted when its category is known.
