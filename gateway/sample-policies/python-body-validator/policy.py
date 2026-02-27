# Copyright (c) 2025, WSO2 LLC. (http://www.wso2.org) All Rights Reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Sample Python policy that validates incoming JSON request bodies against a
JSON Schema provided as a policy parameter.

Uses the third-party ``jsonschema`` library (https://python-jsonschema.readthedocs.io)
to perform validation, demonstrating how to declare and use external dependencies
in a Python policy.

Behaviour
---------
* **Valid body** — passes through to upstream unchanged.
* **Invalid body** — short-circuits with an HTTP 400 response containing a
  JSON error payload that includes the validation error message and the
  failing JSON path.
* **Empty / non-JSON body** — the behaviour is controlled by the
  ``rejectOnMissingBody`` parameter (default ``true``).
* **No schema configured** — passes through without validation (acts as a
  no-op so the policy can be attached without a schema during development).
"""

import json
import logging
from typing import Any, Dict, Optional

import jsonschema
from jsonschema import Draft7Validator, ValidationError

from sdk.policy import (
    Body,
    ImmediateResponse,
    RequestAction,
    RequestContext,
    ResponseAction,
    ResponseContext,
    UpstreamRequestModifications,
)

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _decode_body(body: Optional[Body]) -> tuple[bool, Optional[dict]]:
    """Return (present, parsed_dict).

    ``present`` is True when the body exists and has content.
    ``parsed_dict`` is None when the content is not valid JSON.
    """
    if body is None or not body.present or not body.content:
        return False, None
    try:
        return True, json.loads(body.content.decode("utf-8"))
    except (json.JSONDecodeError, UnicodeDecodeError):
        return True, None


def _error_response(status: int, code: str, message: str, details: str = "") -> ImmediateResponse:
    payload = {"error": {"code": code, "message": message}}
    if details:
        payload["error"]["details"] = details
    return ImmediateResponse(
        status_code=status,
        headers={"content-type": "application/json"},
        body=json.dumps(payload).encode("utf-8"),
    )


# ---------------------------------------------------------------------------
# Policy entry points
# ---------------------------------------------------------------------------

def on_request(ctx: RequestContext, params: Dict[str, Any]) -> RequestAction:
    """Validate the JSON request body against the configured JSON Schema.

    Args:
        ctx: Request context — ``ctx.body`` carries the buffered request body.
        params: Policy parameters.

            * **schema** (*object*, required): A valid JSON Schema (Draft 7)
              object that the request body must conform to.  If absent the
              policy is a no-op.
            * **rejectOnMissingBody** (*boolean*, optional): When ``true``
              (default), requests with no body or a non-JSON body are rejected
              with a 400.  Set to ``false`` to allow such requests through.

    Returns:
        ``None`` (pass-through) on success, or an :class:`ImmediateResponse`
        carrying a ``400`` status and a JSON error payload on failure.
    """
    schema = params.get("schema")
    if not schema:
        # No schema configured — act as a no-op.
        return None

    reject_on_missing = params.get("rejectOnMissingBody", True)

    present, payload = _decode_body(ctx.body)

    if not present or payload is None:
        if reject_on_missing:
            reason = "Request body is missing or empty." if not present else "Request body is not valid JSON."
            return _error_response(400, "INVALID_REQUEST_BODY", reason)
        return None

    # Compile the validator once — jsonschema caches internally per object.
    validator = Draft7Validator(schema)
    errors = sorted(validator.iter_errors(payload), key=lambda e: list(e.path))

    if errors:
        first = errors[0]
        # Build a human-readable JSON path like "$.address.zip"
        path = "$." + ".".join(str(p) for p in first.absolute_path) if first.absolute_path else "$"
        detail = f"[{path}] {first.message}"
        logger.debug("python-body-validator: validation failed — %s", detail)
        return _error_response(
            400,
            "SCHEMA_VALIDATION_FAILED",
            f"Request body failed schema validation. {len(errors)} error(s) found.",
            detail,
        )

    return None  # pass-through


def on_response(ctx: ResponseContext, params: Dict[str, Any]) -> ResponseAction:
    """Pass-through — validation is request-only."""
    return None
