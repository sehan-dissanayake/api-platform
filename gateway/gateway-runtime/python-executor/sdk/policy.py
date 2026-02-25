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

"""Python Policy SDK — mirrors the Go sdk/gateway/policy/v1alpha interface."""

from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from typing import Any, Callable, Dict, List, Optional, Union
from enum import Enum


# Type aliases for stateless policy functions.
# Policy modules should export on_request() and on_response() functions matching these signatures.
OnRequestFn = Callable[["RequestContext", Dict[str, Any]], "RequestAction"]
OnResponseFn = Callable[["ResponseContext", Dict[str, Any]], "ResponseAction"]


# ---------------------- Processing Mode ----------------------


class HeaderProcessingMode(Enum):
    SKIP = "SKIP"
    PROCESS = "PROCESS"


class BodyProcessingMode(Enum):
    SKIP = "SKIP"
    BUFFER = "BUFFER"


@dataclass
class ProcessingMode:
    request_header_mode: HeaderProcessingMode = HeaderProcessingMode.PROCESS
    request_body_mode: BodyProcessingMode = BodyProcessingMode.SKIP
    response_header_mode: HeaderProcessingMode = HeaderProcessingMode.SKIP
    response_body_mode: BodyProcessingMode = BodyProcessingMode.SKIP


# ---------------------- Context Objects ----------------------


@dataclass
class SharedContext:
    project_id: str = ""
    request_id: str = ""
    metadata: Dict[str, Any] = field(default_factory=dict)
    api_id: str = ""
    api_name: str = ""
    api_version: str = ""
    api_kind: str = ""
    api_context: str = ""
    operation_path: str = ""
    auth_context: Dict[str, str] = field(default_factory=dict)


@dataclass
class Body:
    content: Optional[bytes] = None
    end_of_stream: bool = False
    present: bool = False


@dataclass
class RequestContext:
    shared: SharedContext
    headers: Dict[str, str]
    body: Optional[Body] = None
    path: str = ""
    method: str = ""
    authority: str = ""
    scheme: str = ""


@dataclass
class ResponseContext:
    shared: SharedContext
    request_headers: Dict[str, str] = field(default_factory=dict)
    request_body: Optional[Body] = None
    request_path: str = ""
    request_method: str = ""
    response_headers: Dict[str, str] = field(default_factory=dict)
    response_body: Optional[Body] = None
    response_status: int = 200


# ---------------------- Action Types ----------------------


@dataclass
class UpstreamRequestModifications:
    """Continue request to upstream with modifications."""
    set_headers: Dict[str, str] = field(default_factory=dict)
    remove_headers: List[str] = field(default_factory=list)
    append_headers: Dict[str, List[str]] = field(default_factory=dict)
    body: Optional[bytes] = None  # None = no change
    path: Optional[str] = None  # None = no change
    method: Optional[str] = None  # None = no change
    analytics_metadata: Dict[str, Any] = field(default_factory=dict)


@dataclass
class ImmediateResponse:
    """Short-circuit the chain and return response immediately."""
    status_code: int = 500
    headers: Dict[str, str] = field(default_factory=dict)
    body: Optional[bytes] = None
    analytics_metadata: Dict[str, Any] = field(default_factory=dict)


@dataclass
class UpstreamResponseModifications:
    """Modify response from upstream."""
    set_headers: Dict[str, str] = field(default_factory=dict)
    remove_headers: List[str] = field(default_factory=list)
    append_headers: Dict[str, List[str]] = field(default_factory=dict)
    body: Optional[bytes] = None  # None = no change
    status_code: Optional[int] = None  # None = no change
    analytics_metadata: Dict[str, Any] = field(default_factory=dict)


# Union types for return values
RequestAction = Optional[Union[UpstreamRequestModifications, ImmediateResponse]]
ResponseAction = Optional[UpstreamResponseModifications]


# ---------------------- Policy Metadata ----------------------


@dataclass
class PolicyMetadata:
    route_name: str = ""
    api_id: str = ""
    api_name: str = ""
    api_version: str = ""
    attached_to: str = ""  # "api" or "route"


# ---------------------- Policy ABC (Deprecated) ----------------------


class Policy(ABC):
    """Abstract base class for all Python policies.

    .. deprecated::
        The class-based Policy pattern is deprecated. Use stateless functions instead:
        export on_request(ctx, params) and on_response(ctx, params) from your policy module.
        See sample-policies/add-python-header/policy.py for the new pattern.

    The mode() method is not used by the runtime — ProcessingMode is read from
    policy-definition.yaml at build time.
    """

    def __init__(self, metadata: PolicyMetadata, params: Dict[str, Any]):
        """Initialize the policy. Called once per unique (name, version, params_hash) combo.

        Args:
            metadata: Route/API metadata for this policy instance.
            params: Merged system + user parameters (already resolved by Go side).
        """
        self.metadata = metadata
        self.params = params

    def mode(self) -> ProcessingMode:
        """Declare processing requirements. Not used by runtime — defined in policy-definition.yaml."""
        return ProcessingMode()

    @abstractmethod
    def on_request(self, ctx: RequestContext, params: Dict[str, Any]) -> RequestAction:
        """Execute during request phase.

        Args:
            ctx: Request context with headers, body, path, method, shared metadata.
            params: Same as self.params (passed for convenience/compatibility with Go pattern).

        Returns:
            None for pass-through, UpstreamRequestModifications for changes,
            ImmediateResponse for short-circuit.
        """
        ...

    @abstractmethod
    def on_response(self, ctx: ResponseContext, params: Dict[str, Any]) -> ResponseAction:
        """Execute during response phase.

        Args:
            ctx: Response context with request data, response headers/body/status, shared metadata.
            params: Same as self.params.

        Returns:
            None for pass-through, UpstreamResponseModifications for changes.
        """
        ...
