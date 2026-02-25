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

"""Sample Python policy that adds a custom header to responses.

This policy demonstrates the stateless function-based pattern for response
processing. Instead of a class with __init__ and get_policy factory, we export
on_request() and on_response() functions directly.
"""

from typing import Any, Dict

from sdk.policy import (
    RequestContext,
    ResponseContext,
    UpstreamResponseModifications,
    RequestAction,
    ResponseAction,
)


def on_request(ctx: RequestContext, params: Dict[str, Any]) -> RequestAction:
    """Pass-through — no modifications to request.

    Args:
        ctx: Request context with headers, body, path, method, shared metadata.
        params: Policy configuration parameters.

    Returns:
        None for pass-through (no modifications).
    """
    return None


def on_response(ctx: ResponseContext, params: Dict[str, Any]) -> ResponseAction:
    """Add the configured header to the response.

    Args:
        ctx: Response context with request data, response headers/body/status, shared metadata.
        params: Policy configuration parameters (headerName, headerValue).

    Returns:
        DownstreamResponseModifications with the header to add.
    """
    header_name = params.get("headerName", "X-Python-Response-Policy")
    header_value = params.get("headerValue", "hello-from-python-response")
    return UpstreamResponseModifications(
        set_headers={header_name: header_value}
    )
