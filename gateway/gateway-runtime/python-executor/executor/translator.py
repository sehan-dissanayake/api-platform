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

"""Proto ↔ Python object translation."""

import logging
from typing import Any, Dict, Optional

from google.protobuf.struct_pb2 import Struct, Value, ListValue, NullValue

from sdk.policy import (
    Body,
    ImmediateResponse,
    PolicyMetadata,
    RequestAction,
    RequestContext,
    ResponseAction,
    ResponseContext,
    SharedContext,
    UpstreamRequestModifications,
    UpstreamResponseModifications,
)
import proto.python_executor_pb2 as proto

logger = logging.getLogger(__name__)


class Translator:
    """Translates between protobuf messages and Python SDK types."""

    @staticmethod
    def to_python_shared_context(proto_ctx: proto.SharedContext) -> SharedContext:
        """Convert proto SharedContext to Python."""
        return SharedContext(
            project_id=proto_ctx.project_id,
            request_id=proto_ctx.request_id,
            metadata=Translator._proto_struct_to_dict(proto_ctx.metadata),
            api_id=proto_ctx.api_id,
            api_name=proto_ctx.api_name,
            api_version=proto_ctx.api_version,
            api_kind=proto_ctx.api_kind,
            api_context=proto_ctx.api_context,
            operation_path=proto_ctx.operation_path,
            auth_context=dict(proto_ctx.auth_context) if proto_ctx.auth_context else {},
        )

    @staticmethod
    def to_python_request_context(proto_ctx: proto.RequestContext, shared: SharedContext) -> RequestContext:
        """Convert proto RequestContext to Python."""
        body = None
        if proto_ctx.body_present:
            body = Body(
                content=proto_ctx.body if proto_ctx.body else None,
                end_of_stream=proto_ctx.end_of_stream,
                present=True,
            )

        return RequestContext(
            shared=shared,
            headers=dict(proto_ctx.headers) if proto_ctx.headers else {},
            body=body,
            path=proto_ctx.path,
            method=proto_ctx.method,
            authority=proto_ctx.authority,
            scheme=proto_ctx.scheme,
        )

    @staticmethod
    def to_python_response_context(proto_ctx: proto.ResponseContext, shared: SharedContext) -> ResponseContext:
        """Convert proto ResponseContext to Python."""
        request_body = None
        if proto_ctx.request_body:
            request_body = Body(
                content=proto_ctx.request_body if proto_ctx.request_body else None,
                present=True,
            )

        response_body = None
        if proto_ctx.response_body_present:
            response_body = Body(
                content=proto_ctx.response_body if proto_ctx.response_body else None,
                present=True,
            )

        return ResponseContext(
            shared=shared,
            request_headers=dict(proto_ctx.request_headers) if proto_ctx.request_headers else {},
            request_body=request_body,
            request_path=proto_ctx.request_path,
            request_method=proto_ctx.request_method,
            response_headers=dict(proto_ctx.response_headers) if proto_ctx.response_headers else {},
            response_body=response_body,
            response_status=proto_ctx.response_status,
        )

    @staticmethod
    def to_python_policy_metadata(proto_meta: proto.PolicyMetadata) -> PolicyMetadata:
        """Convert proto PolicyMetadata to Python."""
        return PolicyMetadata(
            route_name=proto_meta.route_name,
            api_id=proto_meta.api_id,
            api_name=proto_meta.api_name,
            api_version=proto_meta.api_version,
            attached_to=proto_meta.attached_to,
        )

    @staticmethod
    def to_proto_request_action_result(action: RequestAction) -> proto.RequestActionResult:
        """Convert Python RequestAction to proto."""
        result = proto.RequestActionResult()

        if action is None:
            # Pass-through - return empty result
            return result

        if isinstance(action, UpstreamRequestModifications):
            mod = proto.UpstreamRequestModifications()
            mod.set_headers.update(action.set_headers)
            mod.remove_headers.extend(action.remove_headers)
            for key, values in action.append_headers.items():
                mod.append_headers[key].values.extend(values)
            if action.body is not None:
                mod.body = action.body
                mod.body_present = True
            if action.path is not None:
                mod.path = action.path
                mod.path_present = True
            if action.method is not None:
                mod.method = action.method
                mod.method_present = True
            if action.analytics_metadata:
                mod.analytics_metadata.update(action.analytics_metadata)
            result.continue_request.CopyFrom(mod)

        elif isinstance(action, ImmediateResponse):
            resp = proto.ImmediateResponseAction()
            resp.status_code = action.status_code
            resp.headers.update(action.headers)
            if action.body is not None:
                resp.body = action.body
            if action.analytics_metadata:
                resp.analytics_metadata.update(action.analytics_metadata)
            result.immediate_response.CopyFrom(resp)

        return result

    @staticmethod
    def to_proto_response_action_result(action: ResponseAction) -> proto.ResponseActionResult:
        """Convert Python ResponseAction to proto."""
        result = proto.ResponseActionResult()

        if action is None:
            # Pass-through - return empty result
            return result

        if isinstance(action, UpstreamResponseModifications):
            mod = proto.UpstreamResponseModifications()
            mod.set_headers.update(action.set_headers)
            mod.remove_headers.extend(action.remove_headers)
            for key, values in action.append_headers.items():
                mod.append_headers[key].values.extend(values)
            if action.body is not None:
                mod.body = action.body
                mod.body_present = True
            if action.status_code is not None:
                mod.status_code = action.status_code
                mod.status_code_present = True
            if action.analytics_metadata:
                mod.analytics_metadata.update(action.analytics_metadata)
            result.continue_response.CopyFrom(mod)

        return result

    @staticmethod
    def struct_to_dict(struct: Optional[Struct]) -> Dict[str, Any]:
        """Convert protobuf Struct to Python dict with native Python types."""
        return Translator._proto_struct_to_dict(struct)

    @staticmethod
    def _proto_struct_to_dict(struct: Optional[Struct]) -> Dict[str, Any]:
        """Convert protobuf Struct to Python dict with native Python types."""
        if struct is None:
            return {}
        return {k: Translator._proto_value_to_python(v) for k, v in struct.fields.items()}

    @staticmethod
    def _proto_value_to_python(value: Value) -> Any:
        """Convert a protobuf Value to a native Python type."""
        if value is None:
            return None
        
        # Use WhichOneof to determine the actual type
        kind = value.WhichOneof('kind')
        
        if kind == 'null_value':
            return None
        elif kind == 'number_value':
            return value.number_value
        elif kind == 'string_value':
            return value.string_value
        elif kind == 'bool_value':
            return value.bool_value
        elif kind == 'struct_value':
            return Translator._proto_struct_to_dict(value.struct_value)
        elif kind == 'list_value':
            return [Translator._proto_value_to_python(v) for v in value.list_value.values]
        else:
            return None

    @staticmethod
    def _proto_list_to_python(list_value: ListValue) -> list:
        """Convert a protobuf ListValue to a Python list."""
        if list_value is None:
            return []
        return [Translator._proto_value_to_python(v) for v in list_value.values]
