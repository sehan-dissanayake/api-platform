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

"""gRPC server implementation for Python Executor."""

import asyncio
import logging
import os
import signal
from concurrent import futures
from typing import AsyncIterator, Optional

import grpc
from grpc import aio

from executor.policy_loader import PolicyLoader
from executor.translator import Translator
import proto.python_executor_pb2 as proto
import proto.python_executor_pb2_grpc as proto_grpc

logger = logging.getLogger(__name__)


class PythonExecutorServicer(proto_grpc.PythonExecutorServiceServicer):
    """gRPC servicer for Python Executor."""

    def __init__(self, policy_loader: PolicyLoader, worker_count: int = 4):
        self._loader = policy_loader
        self._translator = Translator()
        self._executor = futures.ThreadPoolExecutor(max_workers=worker_count)
        logger.info(f"PythonExecutorServicer initialized with {worker_count} workers")

    async def ExecuteStream(
        self,
        request_iterator: AsyncIterator[proto.ExecutionRequest],
        context: grpc.ServicerContext
    ) -> AsyncIterator[proto.ExecutionResponse]:
        """Handle bidirectional streaming execution requests."""
        async for request in request_iterator:
            try:
                # Execute policy in thread pool (may be blocking)
                response = await asyncio.get_event_loop().run_in_executor(
                    self._executor,
                    self._execute_policy,
                    request
                )
                yield response
            except Exception as e:
                logger.exception("Error executing policy")
                yield self._error_response(request.request_id, e, request.policy_name, request.policy_version)

    def _execute_policy(self, request: proto.ExecutionRequest) -> proto.ExecutionResponse:
        """Execute a single policy request (runs in thread pool)."""
        # Get policy functions directly from loader (no cache, no instances)
        params = self._translator.struct_to_dict(request.params)
        on_request_fn, on_response_fn = self._loader.get_policy_functions(
            request.policy_name,
            request.policy_version,
        )

        # Build shared context
        shared_ctx = self._translator.to_python_shared_context(request.shared_context)

        # Execute based on phase
        if request.phase == "on_request":
            if on_request_fn is None:
                raise ValueError(f"Policy {request.policy_name}:{request.policy_version} does not implement on_request")

            req_ctx = self._translator.to_python_request_context(request.request_context, shared_ctx)
            action = on_request_fn(req_ctx, params)

            # Merge any metadata changes back
            updated_metadata = self._dict_to_struct(shared_ctx.metadata)

            result = proto.ExecutionResponse(
                request_id=request.request_id,
                request_result=self._translator.to_proto_request_action_result(action),
                updated_metadata=updated_metadata,
            )
            return result

        elif request.phase == "on_response":
            if on_response_fn is None:
                raise ValueError(f"Policy {request.policy_name}:{request.policy_version} does not implement on_response")

            resp_ctx = self._translator.to_python_response_context(request.response_context, shared_ctx)
            action = on_response_fn(resp_ctx, params)

            # Merge any metadata changes back
            updated_metadata = self._dict_to_struct(shared_ctx.metadata)

            result = proto.ExecutionResponse(
                request_id=request.request_id,
                response_result=self._translator.to_proto_response_action_result(action),
                updated_metadata=updated_metadata,
            )
            return result

        else:
            raise ValueError(f"Unknown phase: {request.phase}")

    def _error_response(
        self,
        request_id: str,
        error: Exception,
        policy_name: str,
        policy_version: str
    ) -> proto.ExecutionResponse:
        """Create an error response."""
        # No more init phase — all errors are execution errors in stateless model
        error_type = "execution_error"
        return proto.ExecutionResponse(
            request_id=request_id,
            error=proto.ExecutionError(
                message=str(error),
                policy_name=policy_name,
                policy_version=policy_version,
                error_type=error_type,
            )
        )

    @staticmethod
    def _dict_to_struct(d: dict):
        """Convert dict to protobuf Struct."""
        from google.protobuf.struct_pb2 import Struct
        s = Struct()
        if d:
            s.update(d)
        return s

    async def HealthCheck(
        self,
        request: proto.HealthCheckRequest,
        context: grpc.ServicerContext
    ) -> proto.HealthCheckResponse:
        """Health check endpoint."""
        loaded = self._loader.get_loaded_policy_count()
        return proto.HealthCheckResponse(
            ready=True,
            loaded_policies=loaded
        )


class PythonExecutorServer:
    """Python Executor gRPC server."""

    def __init__(self, socket_path: str, worker_count: int = 4):
        self.socket_path = socket_path
        self.worker_count = worker_count
        self.server: Optional[aio.Server] = None
        self._loader = PolicyLoader()

    async def start(self):
        """Start the server."""
        logger.info(f"Starting Python Executor on {self.socket_path}")

        # Load policies
        loaded_count = self._loader.load_policies()
        logger.info(f"Loaded {loaded_count} policies")

        # Create server
        self.server = aio.server(
            migration_thread_pool=futures.ThreadPoolExecutor(max_workers=self.worker_count),
            options=[
                ('grpc.max_send_message_length', 50 * 1024 * 1024),  # 50MB
                ('grpc.max_receive_message_length', 50 * 1024 * 1024),  # 50MB
            ]
        )

        # Add servicer
        servicer = PythonExecutorServicer(self._loader, self.worker_count)
        proto_grpc.add_PythonExecutorServiceServicer_to_server(servicer, self.server)

        # Bind to Unix Domain Socket
        if os.path.exists(self.socket_path):
            os.remove(self.socket_path)

        self.server.add_insecure_port(f"unix:{self.socket_path}")
        await self.server.start()

        logger.info(f"Python Executor ready on {self.socket_path}")

    async def wait_for_termination(self):
        """Wait for the server to terminate."""
        if self.server:
            await self.server.wait_for_termination()

    async def shutdown(self):
        """Shutdown the server gracefully."""
        logger.info("Shutting down Python Executor...")
        if self.server:
            await self.server.stop(grace_period=5)
        logger.info("Python Executor shutdown complete")
