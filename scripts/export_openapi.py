"""Write the API's OpenAPI spec to openapi/payahub.v1.json (the source for docs and SDKs)."""

import json
from pathlib import Path

from payahub_api.main import app

out = Path(__file__).resolve().parent.parent / "openapi" / "payahub.v1.json"
out.write_text(json.dumps(app.openapi(), indent=2) + "\n")
print(f"wrote {out}")
