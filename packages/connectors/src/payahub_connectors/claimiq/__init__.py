"""ClaimIQ connector — manifest only for now.

The adapter is ported in phase 0 from serenity-health/core-api-v2
backend/app/plugins/insurance/claimiq.py. Things that code learned the hard
way and the port must keep:

- Filing a claim does not submit it. POST /v1/claims (or incremental
  item/diagnosis calls) only queues it in ClaimIQ; nothing reaches the payer
  until POST /v1/batches runs. submit_whole_claim therefore creates the claim
  and sends a batch of one.
- Opening an empty claim shell goes through POST /patients/check-in with
  create_claim=true, not POST /claims, which requires claim data up front.
"""

from __future__ import annotations

from ..sdk import Connector, ConnectorManifest, CredentialField, SubmissionConfig, Verb


class ClaimIQConnector(Connector):
    manifest = ConnectorManifest(
        key="claimiq",
        name="ClaimIQ",
        country="GH",
        verbs=frozenset(
            {
                Verb.VERIFY_MEMBER,
                Verb.REQUEST_AUTHORIZATION,
                Verb.SUBMIT_WHOLE_CLAIM,
                Verb.OPEN_CLAIM,
                Verb.ADD_LINE,
                Verb.UPDATE_LINE,
                Verb.DELETE_LINE,
                Verb.ADD_DIAGNOSIS,
                Verb.FINALIZE_CLAIM,
                Verb.WITHDRAW_CLAIM,
                Verb.FETCH_CLAIM,
            }
        ),
        credentials=[
            CredentialField(name="base_url", label="API base URL"),
            CredentialField(name="api_key", label="API key", secret=True),
        ],
        submission=SubmissionConfig(line_submission_mode="bulk", claim_creation_timing="deferred"),
    )
