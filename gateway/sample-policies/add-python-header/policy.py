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

"""Sample Python policy that adds a custom header to requests.

This policy demonstrates the stateless function-based pattern.
Instead of a class with __init__ and get_policy factory, we export
on_request() and on_response() functions directly.
"""

from typing import Any, Dict

from sdk.policy import (
    RequestContext,
    ResponseContext,
    UpstreamRequestModifications,
    RequestAction,
    ResponseAction,
)


def on_request(ctx: RequestContext, params: Dict[str, Any]) -> RequestAction:
    """Add the configured header to the request.

    Args:
        ctx: Request context with headers, body, path, method, shared metadata.
        params: Policy configuration parameters (headerName, headerValue).

    Returns:
        UpstreamRequestModifications with the header to add.
    """
    header_name = params.get("headerName", "X-Python-Policy")
    header_value = params.get("headerValue", "hello-from-python")
    return UpstreamRequestModifications(
        set_headers={header_name: header_value}
    )


def on_response(ctx: ResponseContext, params: Dict[str, Any]) -> ResponseAction:
    """Pass-through — no modifications to response.

    Args:
        ctx: Response context with request data, response headers/body/status, shared metadata.
        params: Policy configuration parameters.

    Returns:
        None for pass-through (no modifications).
    """
    return None
