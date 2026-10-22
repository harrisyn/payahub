`icd10cm.tsv.gz` is the ICD-10-CM code list (code without its dot, 1 when the
code is billable at that level, title), from the U.S. National Center for
Health Statistics / CMS release, which is in the public domain. Ghana's
payers use WHO ICD-10, which shares its categories with ICD-10-CM except a
few; `who.tsv` adds the WHO-only codes in common use. A WHO subcode missing
here is accepted when its category is known.
