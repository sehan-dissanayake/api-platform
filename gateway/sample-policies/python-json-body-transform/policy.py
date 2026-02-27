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

"""Sample Python policy that transforms JSON request and response bodies.

Demonstrates reading, modifying, and replacing body content using the
stateless function-based pattern.

Request phase:
    Parses the JSON request body and injects the key-value pairs defined in
    the ``injectFields`` parameter before forwarding to the upstream service.

Response phase:
    Parses the JSON response body and replaces the values of fields listed in
    ``maskFields`` with a configurable mask string (default ``"****"``), useful
    for redacting sensitive data (e.g. passwords, tokens) in logged or forwarded
    responses.

If the body is absent, empty, or not valid JSON the policy passes through
without modification and does not raise an error.
"""

import json
import logging
from typing import Any, Dict, List, Optional

from sdk.policy import (
    Body,
    ImmediateResponse,
    RequestAction,
    RequestContext,
    ResponseAction,
    ResponseContext,
    UpstreamRequestModifications,
    UpstreamResponseModifications,
)

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------

def _decode_body(body: Optional[Body]) -> Optional[dict]:
    """Decode a Body to a Python dict.

    Returns None when the body is absent, empty, or not valid JSON so that
    callers can fall back to pass-through behaviour gracefully.
    """
    if body is None or not body.present or not body.content:
        return None
    try:
        return json.loads(body.content.decode("utf-8"))
    except (json.JSONDecodeError, UnicodeDecodeError) as exc:
        logger.debug("json-body-transform: body is not valid JSON, skipping — %s", exc)
        return None


def _encode_body(data: dict) -> bytes:
    """Encode a Python dict back to compact UTF-8 JSON bytes."""
    return json.dumps(data, separators=(",", ":"), ensure_ascii=False).encode("utf-8")


# ---------------------------------------------------------------------------
# Policy entry points
# ---------------------------------------------------------------------------

def on_request(ctx: RequestContext, params: Dict[str, Any]) -> RequestAction:
    """Inject extra fields into the JSON request body.

    Reads the ``injectFields`` parameter (a mapping of field names to values)
    and merges those entries into the top-level JSON object.  Existing fields
    are overwritten so that the injected values take precedence.

    Args:
        ctx: Request context — ``ctx.body`` carries the buffered request body.
        params: Policy parameters.

            * **injectFields** (*object*, optional): Key-value pairs to inject
              into the request JSON body.  Defaults to ``{}`` (no-op).

    Returns:
        :class:`UpstreamRequestModifications` with the updated body, or
        ``None`` (pass-through) when there is nothing to inject or the body
        is not JSON.
    """
    inject_fields: Dict[str, Any] = params.get("injectFields", {})
    if not inject_fields:
        return None

    payload = _decode_body(ctx.body)
    if payload is None:
        # Body absent or not JSON — skip silently.
        return None

    payload.update(inject_fields)

    return UpstreamRequestModifications(
        body=_encode_body(payload),
        set_headers={"content-type": "application/json"},
    )


def on_response(ctx: ResponseContext, params: Dict[str, Any]) -> ResponseAction:
    """Mask sensitive fields in the JSON response body.

    Iterates over the ``maskFields`` list and replaces matching top-level key
    values in the response JSON with the ``maskValue`` string so that sensitive
    data is not leaked to downstream consumers.

    Args:
        ctx: Response context — ``ctx.response_body`` carries the buffered
             response body.
        params: Policy parameters.

            * **maskFields** (*array of strings*, optional): Names of top-level
              JSON fields whose values should be replaced.  Defaults to ``[]``
              (no-op).
            * **maskValue** (*string*, optional): Replacement string written
              over masked field values.  Defaults to ``"****"``.

    Returns:
        :class:`UpstreamResponseModifications` with the updated body, or
        ``None`` (pass-through) when there is nothing to mask or the body
        is not JSON.
    """
    mask_fields: List[str] = params.get("maskFields", [])
    if not mask_fields:
        return None

    mask_value: str = params.get("maskValue", "****")

    payload = _decode_body(ctx.response_body)
    if payload is None:
        return None

    modified = False
    for field in mask_fields:
        if field in payload:
            payload[field] = mask_value
            modified = True

    if not modified:
        return None

    return UpstreamResponseModifications(
        body=_encode_body(payload),
        set_headers={"content-type": "application/json"},
    )
