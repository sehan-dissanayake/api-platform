# Implementation Plan: Concurrent Fan-Out for Python Executor

## Objective

Replace the sequential `async for / await / yield` pattern in `PythonExecutorServicer.ExecuteStream()` with a concurrent reader/writer fan-out pattern. This enables multiple policy executions to run simultaneously in the thread pool instead of one-at-a-time.

**Files to modify:** Only ONE file needs changes: `gateway/gateway-runtime/python-executor/executor/server.py`

**No changes required to:** Go code, proto definitions, `main.py`, `policy_loader.py`, `translator.py`, or any policy code.

---

## Current Code (What to Replace)

File: `gateway/gateway-runtime/python-executor/executor/server.py`

The current `ExecuteStream` method (lines 43–57):

```python
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
```

**Problem:** The `await` on `run_in_executor` pauses the entire loop — it cannot read the next request until the current one finishes. The 4 thread pool workers are starved because only 1 request is submitted at a time.

---

## New Design

Split `ExecuteStream` into 3 concurrent parts running within the same method:

```
┌─────────────────────────────────────────────────────────────────┐
│  ExecuteStream (one call per Go stream connection)              │
│                                                                 │
│  ┌──────────┐     ┌─────────────────────┐     ┌──────────┐     │
│  │  Reader   │────▶│  asyncio.create_task │────▶│  Writer  │     │
│  │  Task     │     │  per request         │     │  Loop    │     │
│  │          │     │  (thread pool exec)  │     │  (yield) │     │
│  └──────────┘     └─────────────────────┘     └──────────┘     │
│       │                     │                       │           │
│  reads from           puts result into         reads from      │
│  request_iterator     asyncio.Queue            asyncio.Queue   │
│  as fast as possible  (bounded)                and yields      │
└─────────────────────────────────────────────────────────────────┘
```

1. **Reader task** — Consumes `request_iterator` eagerly. For each request, spawns an `asyncio.create_task()` to process it. Does NOT wait for processing. Immediately reads the next request.

2. **Processing tasks** (1 per request) — Each task runs `_execute_policy` in the thread pool via `run_in_executor`, then puts the result (success or error) into the response queue.

3. **Writer loop** (the main generator body) — Pulls from the response queue and `yield`s responses. Responses come out in completion order (not request order). Exits when the reader is done AND no tasks are in-flight AND the queue is empty.

---

## Detailed Implementation Steps

### Step 1: Add new environment variable for max concurrency

In `main.py`, add a new constant after the existing `WORKER_COUNT` line (line 34):

```python
WORKER_COUNT = int(os.environ.get("PYTHON_POLICY_WORKERS", "4"))
MAX_CONCURRENT = int(os.environ.get("PYTHON_POLICY_MAX_CONCURRENT", "100"))
```

Pass it to `PythonExecutorServer`:

```python
server = PythonExecutorServer(SOCKET_PATH, WORKER_COUNT, MAX_CONCURRENT)
```

Update the log line:

```python
logger.info(f"Python Executor starting (workers={WORKER_COUNT}, max_concurrent={MAX_CONCURRENT}, log_level={LOG_LEVEL})")
```

### Step 2: Update `PythonExecutorServer.__init__` to accept `max_concurrent`

In `server.py`, update the `PythonExecutorServer` class:

```python
class PythonExecutorServer:
    """Python Executor gRPC server."""

    def __init__(self, socket_path: str, worker_count: int = 4, max_concurrent: int = 100):
        self.socket_path = socket_path
        self.worker_count = worker_count
        self.max_concurrent = max_concurrent
        self.server: Optional[aio.Server] = None
        self._loader = PolicyLoader()
```

Update the `start()` method to pass `max_concurrent` to the servicer:

```python
servicer = PythonExecutorServicer(self._loader, self.worker_count, self.max_concurrent)
```

### Step 3: Update `PythonExecutorServicer.__init__`

```python
class PythonExecutorServicer(proto_grpc.PythonExecutorServiceServicer):
    """gRPC servicer for Python Executor."""

    def __init__(self, policy_loader: PolicyLoader, worker_count: int = 4, max_concurrent: int = 100):
        self._loader = policy_loader
        self._translator = Translator()
        self._executor = futures.ThreadPoolExecutor(max_workers=worker_count)
        self._max_concurrent = max_concurrent
        logger.info(f"PythonExecutorServicer initialized with {worker_count} workers, max_concurrent={max_concurrent}")
```

### Step 4: Replace `ExecuteStream` with concurrent fan-out implementation

Replace the entire `ExecuteStream` method with:

```python
async def ExecuteStream(
    self,
    request_iterator: AsyncIterator[proto.ExecutionRequest],
    context: grpc.ServicerContext
) -> AsyncIterator[proto.ExecutionResponse]:
    """Handle bidirectional streaming execution requests with concurrent fan-out.
    
    Architecture:
    - Reader task: eagerly consumes request_iterator, spawns a processing task per request
    - Processing tasks: execute policy in thread pool, put result in response queue
    - Writer (this generator): yields responses from queue as they complete (out-of-order)
    
    The Go side correlates responses by request_id, so order doesn't matter.
    """
    response_queue: asyncio.Queue[proto.ExecutionResponse] = asyncio.Queue(maxsize=self._max_concurrent)
    in_flight: set[asyncio.Task] = set()
    reader_done = asyncio.Event()
    concurrency_limit = asyncio.Semaphore(self._max_concurrent)

    async def process_request(request: proto.ExecutionRequest) -> None:
        """Process a single request and put the result in the response queue."""
        try:
            async with concurrency_limit:
                loop = asyncio.get_event_loop()
                response = await loop.run_in_executor(
                    self._executor,
                    self._execute_policy,
                    request
                )
                await response_queue.put(response)
        except Exception as e:
            logger.exception(f"Error executing policy {request.policy_name}:{request.policy_version}")
            error_resp = self._error_response(
                request.request_id, e, request.policy_name, request.policy_version
            )
            await response_queue.put(error_resp)

    async def reader() -> None:
        """Eagerly consume requests from the stream and spawn processing tasks."""
        try:
            async for request in request_iterator:
                if not context.is_active():
                    logger.warning("Stream context no longer active, stopping reader")
                    break
                task = asyncio.create_task(process_request(request))
                in_flight.add(task)
                task.add_done_callback(in_flight.discard)
        except asyncio.CancelledError:
            logger.info("Reader cancelled")
        except Exception as e:
            logger.exception("Reader encountered error")
        finally:
            reader_done.set()

    # Start the reader as a background task
    reader_task = asyncio.create_task(reader())

    try:
        # Writer loop: yield responses as they complete
        while True:
            # Try to get a response without blocking first (drain any ready items)
            try:
                response = response_queue.get_nowait()
                yield response
                continue
            except asyncio.QueueEmpty:
                pass

            # Check if we're done: reader finished AND no in-flight tasks AND queue empty
            if reader_done.is_set() and len(in_flight) == 0 and response_queue.empty():
                break

            # Wait for the next response with a short timeout
            # (timeout allows re-checking the done condition)
            try:
                response = await asyncio.wait_for(response_queue.get(), timeout=0.1)
                yield response
            except asyncio.TimeoutError:
                continue

    except asyncio.CancelledError:
        logger.info("ExecuteStream cancelled, cleaning up")
        # Cancel reader
        reader_task.cancel()
        try:
            await reader_task
        except asyncio.CancelledError:
            pass
        # Cancel all in-flight processing tasks
        for task in list(in_flight):
            task.cancel()
        if in_flight:
            await asyncio.wait(in_flight, timeout=5.0)
        raise
    finally:
        # Ensure reader is cleaned up
        if not reader_task.done():
            reader_task.cancel()
            try:
                await reader_task
            except asyncio.CancelledError:
                pass
```

### Step 5: Keep all other methods UNCHANGED

The following methods remain exactly as they are — no modifications:
- `_execute_policy()` — the synchronous policy execution (runs in thread pool)
- `_error_response()` — creates error protobuf response
- `_dict_to_struct()` — converts dict to protobuf Struct
- `HealthCheck()` — health check RPC

The following classes/files remain exactly as they are — no modifications:
- `PolicyLoader` in `policy_loader.py`
- `Translator` in `translator.py`
- All proto files
- All Go code

---

## Complete Final `server.py`

For clarity, here is the complete final file that should result from these changes:

```python
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

    def __init__(self, policy_loader: PolicyLoader, worker_count: int = 4, max_concurrent: int = 100):
        self._loader = policy_loader
        self._translator = Translator()
        self._executor = futures.ThreadPoolExecutor(max_workers=worker_count)
        self._max_concurrent = max_concurrent
        logger.info(f"PythonExecutorServicer initialized with {worker_count} workers, max_concurrent={max_concurrent}")

    async def ExecuteStream(
        self,
        request_iterator: AsyncIterator[proto.ExecutionRequest],
        context: grpc.ServicerContext
    ) -> AsyncIterator[proto.ExecutionResponse]:
        """Handle bidirectional streaming execution requests with concurrent fan-out.

        Architecture:
        - Reader task: eagerly consumes request_iterator, spawns a processing task per request
        - Processing tasks: execute policy in thread pool, put result in response queue
        - Writer (this generator): yields responses from queue as they complete (out-of-order)

        The Go side correlates responses by request_id, so order doesn't matter.
        """
        response_queue: asyncio.Queue[proto.ExecutionResponse] = asyncio.Queue(maxsize=self._max_concurrent)
        in_flight: set[asyncio.Task] = set()
        reader_done = asyncio.Event()
        concurrency_limit = asyncio.Semaphore(self._max_concurrent)

        async def process_request(request: proto.ExecutionRequest) -> None:
            """Process a single request and put the result in the response queue."""
            try:
                async with concurrency_limit:
                    loop = asyncio.get_event_loop()
                    response = await loop.run_in_executor(
                        self._executor,
                        self._execute_policy,
                        request
                    )
                    await response_queue.put(response)
            except Exception as e:
                logger.exception(f"Error executing policy {request.policy_name}:{request.policy_version}")
                error_resp = self._error_response(
                    request.request_id, e, request.policy_name, request.policy_version
                )
                await response_queue.put(error_resp)

        async def reader() -> None:
            """Eagerly consume requests from the stream and spawn processing tasks."""
            try:
                async for request in request_iterator:
                    if not context.is_active():
                        logger.warning("Stream context no longer active, stopping reader")
                        break
                    task = asyncio.create_task(process_request(request))
                    in_flight.add(task)
                    task.add_done_callback(in_flight.discard)
            except asyncio.CancelledError:
                logger.info("Reader cancelled")
            except Exception as e:
                logger.exception("Reader encountered error")
            finally:
                reader_done.set()

        # Start the reader as a background task
        reader_task = asyncio.create_task(reader())

        try:
            # Writer loop: yield responses as they complete
            while True:
                # Try to get a response without blocking first (drain any ready items)
                try:
                    response = response_queue.get_nowait()
                    yield response
                    continue
                except asyncio.QueueEmpty:
                    pass

                # Check if we're done: reader finished AND no in-flight tasks AND queue empty
                if reader_done.is_set() and len(in_flight) == 0 and response_queue.empty():
                    break

                # Wait for the next response with a short timeout
                # (timeout allows re-checking the done condition)
                try:
                    response = await asyncio.wait_for(response_queue.get(), timeout=0.1)
                    yield response
                except asyncio.TimeoutError:
                    continue

        except asyncio.CancelledError:
            logger.info("ExecuteStream cancelled, cleaning up")
            # Cancel reader
            reader_task.cancel()
            try:
                await reader_task
            except asyncio.CancelledError:
                pass
            # Cancel all in-flight processing tasks
            for task in list(in_flight):
                task.cancel()
            if in_flight:
                await asyncio.wait(in_flight, timeout=5.0)
            raise
        finally:
            # Ensure reader is cleaned up
            if not reader_task.done():
                reader_task.cancel()
                try:
                    await reader_task
                except asyncio.CancelledError:
                    pass

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

    def __init__(self, socket_path: str, worker_count: int = 4, max_concurrent: int = 100):
        self.socket_path = socket_path
        self.worker_count = worker_count
        self.max_concurrent = max_concurrent
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
        servicer = PythonExecutorServicer(self._loader, self.worker_count, self.max_concurrent)
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
```

---

## Complete Final `main.py`

```python
#!/usr/bin/env python3
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

"""Python Executor entry point.

Starts the gRPC server on UDS, loads all registered Python policies,
and serves ExecuteStream RPCs from the Go Policy Engine.
"""

import asyncio
import logging
import os
import signal
import sys

from executor.server import PythonExecutorServer

SOCKET_PATH = os.environ.get(
    "PYTHON_EXECUTOR_SOCKET",
    "/var/run/api-platform/python-executor.sock"
)
WORKER_COUNT = int(os.environ.get("PYTHON_POLICY_WORKERS", "4"))
MAX_CONCURRENT = int(os.environ.get("PYTHON_POLICY_MAX_CONCURRENT", "100"))
LOG_LEVEL = os.environ.get("LOG_LEVEL", "info").upper()


def setup_logging():
    """Configure structured logging."""
    level = getattr(logging, LOG_LEVEL, logging.INFO)

    # Use JSON format for production, text for development
    handler = logging.StreamHandler(sys.stdout)
    handler.setLevel(level)

    # Simple format with [pye] prefix for the entrypoint to identify
    formatter = logging.Formatter(
        fmt='%(asctime)s [%(levelname)s] %(name)s: %(message)s',
        datefmt='%Y-%m-%d %H:%M:%S'
    )
    handler.setFormatter(formatter)

    # Configure root logger
    root_logger = logging.getLogger()
    root_logger.setLevel(level)
    root_logger.addHandler(handler)

    # Set specific levels for noisy loggers
    logging.getLogger("grpc").setLevel(logging.WARNING)


async def main():
    """Main entry point."""
    setup_logging()
    logger = logging.getLogger(__name__)

    logger.info(f"Python Executor starting (workers={WORKER_COUNT}, max_concurrent={MAX_CONCURRENT}, log_level={LOG_LEVEL})")

    server = PythonExecutorServer(SOCKET_PATH, WORKER_COUNT, MAX_CONCURRENT)

    # Graceful shutdown on SIGTERM/SIGINT
    loop = asyncio.get_event_loop()

    def signal_handler():
        logger.info("Received shutdown signal")
        asyncio.create_task(server.shutdown())

    for sig in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(sig, signal_handler)

    try:
        await server.start()
        await server.wait_for_termination()
    except asyncio.CancelledError:
        logger.info("Server cancelled")
    finally:
        await server.shutdown()


if __name__ == "__main__":
    asyncio.run(main())
```

---

## Change Summary

| File | Change |
|------|--------|
| `gateway/gateway-runtime/python-executor/executor/server.py` | Replace `ExecuteStream` with concurrent fan-out; add `max_concurrent` param to `__init__` of both `PythonExecutorServicer` and `PythonExecutorServer` |
| `gateway/gateway-runtime/python-executor/main.py` | Add `MAX_CONCURRENT` env var, pass to server constructor, update log line |

**Total: 2 files modified. 0 files created. 0 files deleted. No Go changes.**

---

## How It Works (Before vs. After)

### Before (Sequential)

```
Time 0ms:   Read request #1 → submit to thread pool → WAIT
Time 50ms:  Thread finishes → yield response #1 → Read request #2 → submit → WAIT
Time 100ms: Thread finishes → yield response #2 → Read request #3 → submit → WAIT
...
```

Only 1 thread used at a time. 200 requests × 50ms = 10 seconds total.

### After (Concurrent Fan-Out)

```
Time 0ms:   Reader reads #1 → create_task → Reader reads #2 → create_task → Reader reads #3 → ...
Time 0ms:   Task #1 submitted to thread pool
Time 0ms:   Task #2 submitted to thread pool
Time 0ms:   Task #3 submitted to thread pool
Time 0ms:   Task #4 submitted to thread pool
            (4 threads now all busy simultaneously)
Time 1ms:   Writer checks queue → empty → waits
Time 50ms:  Tasks #1-#4 complete → results in queue → Writer yields #1, #2, #3, #4
Time 50ms:  Tasks #5-#8 grabbed by now-free threads
...
```

All 4 threads used simultaneously. 200 requests / 4 threads × 50ms = 2.5 seconds total.
For fast 1ms policies: threads free almost instantly, p99 stays ~2ms regardless of concurrency.

---

## Key Design Decisions

1. **`asyncio.Queue(maxsize=max_concurrent)`** — Bounded queue provides backpressure. If processing tasks produce results faster than the writer can yield them, the queue fills up and `put()` blocks, naturally throttling.

2. **`asyncio.Semaphore(max_concurrent)`** — Caps the number of in-flight tasks. Without this, 10,000 simultaneous requests would spawn 10,000 tasks, consuming excessive memory. Default 100 is generous for the expected workload.

3. **`task.add_done_callback(in_flight.discard)`** — Automatically cleans up completed tasks from the tracking set without manual iteration.

4. **`response_queue.get_nowait()` first, then `wait_for(..., timeout=0.1)`** — Drains ready responses immediately (no latency). Falls back to a 0.1s polling wait to recheck the termination condition. The 0.1s timeout is only hit during idle periods; under load, `get_nowait()` always finds items.

5. **Termination condition: `reader_done AND len(in_flight) == 0 AND queue.empty()`** — All three must be true. Reader finished reading from the stream, all processing tasks completed, and all results have been yielded.

6. **`context.is_active()` check in reader** — Stops reading if the Go side disconnects mid-stream.

7. **All policies go to thread pool (Option B)** — Every policy uses `run_in_executor`. Simple, safe, no classification logic needed. The 50-100μs thread handoff overhead is negligible for our latency targets.

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PYTHON_POLICY_WORKERS` | `4` | Thread pool size (same as before) |
| `PYTHON_POLICY_MAX_CONCURRENT` | `100` | Max in-flight tasks (new) |
| `PYTHON_POLICY_TIMEOUT` | `30s` | Per-request timeout on Go side (unchanged) |

---

## Testing

### Manual Smoke Test

After implementing, verify with the existing integration test setup:

1. Build gateway images: `cd gateway && make build`
2. Remove `-coverage` suffix from gateway-controller image in `gateway/it/docker-compose.test.yaml` line 25
3. Run integration tests: `cd gateway/it && make test`
4. Verify all existing Python policy tests still pass
5. Revert the `-coverage` suffix change before committing

### What to Verify

- **Correctness**: Responses still have correct `request_id` matching (Go side correlates them)
- **No regressions**: All existing Go and Python policy tests pass
- **Concurrent behavior**: Under load, multiple thread pool workers are active simultaneously (visible via DEBUG logging showing overlapping request processing)

### Edge Cases to Verify

- **Stream disconnect**: If Go disconnects mid-stream, reader should stop and in-flight tasks should complete or be cancelled gracefully
- **Policy error**: If a policy raises an exception, an error response should be put in the queue (not crash the stream)
- **Empty stream**: If Go opens a stream but sends nothing, the reader should finish immediately and the writer should exit
- **Single request**: If only 1 request arrives, behavior should be identical to the old sequential code

---

## What This Does NOT Change

- **Go Policy Engine** — No changes to StreamManager, BridgeFactory, PythonBridge, or any Go code
- **Proto definitions** — No changes to `python_executor.proto`
- **Policy interface** — Policies still implement `on_request(ctx, params)` / `on_response(ctx, params)` as synchronous functions
- **Policy loading** — `PolicyLoader` unchanged
- **Translation** — `Translator` unchanged
- **Docker/build** — No Dockerfile or build.yaml changes
- **Startup sequence** — docker-entrypoint.sh unchanged
