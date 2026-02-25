# Implementation Plan: Make Python Executor Stateless

## Goal

Remove the instance-based `Policy` class pattern from the Python side. Replace it with **stateless function dispatch**: each policy module exports `on_request()` and `on_response()` functions instead of a class. Delete `PolicyCache` entirely. The Go side requires **zero changes**.

---

## Why

- The current `PolicyCache` creates one class instance per unique `(name, version, hash(params))` and reuses it across concurrent requests. This introduces threading risks (shared mutable `self`), lock contention, and complexity — all for zero benefit since `params` is already passed to every call anyway.
- A stateless function-per-policy model is simpler, thread-safe by design, and more Pythonic.

---

## Scope

| Layer | Changes? |
|-------|----------|
| Python SDK (`sdk/policy.py`, `sdk/__init__.py`) | **Yes** — keep dataclasses/types, deprecate `Policy` ABC, add new function type aliases |
| Python Executor (`executor/server.py`) | **Yes** — remove cache usage, call functions directly |
| Python Executor (`executor/policy_loader.py`) | **Yes** — load `on_request`/`on_response` functions instead of `get_policy` factory |
| Python Executor (`executor/policy_cache.py`) | **Delete entirely** |
| Python Executor (`executor/metrics.py`) | No changes |
| Python Executor (`executor/translator.py`) | No changes |
| Python Executor (`main.py`) | **Yes** — remove `PolicyCache` construction |
| Proto definition (`python_executor.proto`) | No changes |
| Go Python Bridge (`pythonbridge/`) | **No changes** |
| Go Policy Engine | **No changes** |
| Gateway Builder (`generator.go`) | **Yes** — update generated registry format |
| Sample policy (`add-python-header/policy.py`) | **Yes** — convert from class to functions |
| Docs (`docs/python-policy-support.md`) | **Yes** — update description |

---

## File-by-File Changes

### 1. `gateway/gateway-runtime/python-executor/sdk/policy.py`

**What to change:**

- **Keep all dataclasses unchanged**: `ProcessingMode`, `HeaderProcessingMode`, `BodyProcessingMode`, `SharedContext`, `Body`, `RequestContext`, `ResponseContext`, `UpstreamRequestModifications`, `ImmediateResponse`, `UpstreamResponseModifications`, `PolicyMetadata`. These are the data types — they are not part of the instance pattern.
- **Keep the type aliases unchanged**: `RequestAction`, `ResponseAction`.
- **Keep the `Policy` ABC** but mark it as **deprecated** with a docstring note. This is for backward compatibility — existing policy authors who used the class pattern can still use it. Do NOT delete it yet.
- **Add new function type aliases** after the `Policy` class:

```python
from typing import Callable

# Stateless function signatures for Python policies.
# Policy modules should export functions matching these signatures.
OnRequestFn = Callable[[RequestContext, Dict[str, Any]], RequestAction]
OnResponseFn = Callable[[ResponseContext, Dict[str, Any]], ResponseAction]
```

- **Remove** the `mode()` method from `Policy` class — it was never called by the Python Executor. The Go side reads `ProcessingMode` from `policy-definition.yaml` at build time.
  - Actually, keep it for backward compat but note in docstring it's unused by the runtime.

**Net effect:** Only additions (new type aliases) and docstring changes. No breaking changes.

---

### 2. `gateway/gateway-runtime/python-executor/sdk/__init__.py`

**What to change:**

- Add `OnRequestFn` and `OnResponseFn` to the imports and `__all__` list.

---

### 3. `gateway/gateway-runtime/python-executor/executor/policy_loader.py`

**Current behavior:** Loads each policy module, looks for `get_policy` callable (a factory that returns a `Policy` instance), stores factories in `self._factories` dict.

**New behavior:** Loads each policy module, looks for `on_request` and `on_response` callables (plain functions), stores them as tuples.

**Detailed changes:**

- Remove import of `Policy` and `PolicyMetadata` from `sdk.policy` (no longer needed).
- Remove the `PolicyFactory` type alias.
- Change `self._factories: Dict[str, PolicyFactory]` to `self._functions: Dict[str, Tuple[Callable, Callable]]` — each entry maps `"name:version"` → `(on_request_fn, on_response_fn)`.
- In `_load_policy()`:
  - Instead of looking for `module.get_policy`, look for `module.on_request` and `module.on_response`.
  - Validate both are callable.
  - Store as tuple: `self._functions[policy_key] = (module.on_request, module.on_response)`.
  - **Backward compatibility fallback**: If `on_request`/`on_response` are not found at module level but `get_policy` exists, log a deprecation warning and wrap it. Create a temporary instance to extract the functions. This allows old-style class-based policies to still work during migration:
    ```python
    # Backward compat: if module has get_policy but no on_request/on_response
    if hasattr(module, 'get_policy') and not hasattr(module, 'on_request'):
        logger.warning(f"Policy {policy_key} uses deprecated class-based pattern. "
                       "Migrate to on_request()/on_response() functions.")
        factory = module.get_policy
        # Create a dummy instance just to get the functions
        dummy_metadata = PolicyMetadata()
        dummy_instance = factory(dummy_metadata, {})
        self._functions[policy_key] = (dummy_instance.on_request, dummy_instance.on_response)
    ```
    **IMPORTANT**: This backward compat wrapper still has the shared-instance problem. It is meant only as a migration bridge. Log the warning clearly.
- Rename `get_factory()` to `get_functions()` — returns `Tuple[Callable, Callable]` (on_request, on_response).
  - Or keep two methods: `get_on_request(name, version)` and `get_on_response(name, version)`.
  - Simpler: just `get_policy_functions(name, version) -> Tuple[OnRequestFn, OnResponseFn]`.
- Update `get_loaded_policies()` — no signature change needed, still returns policy keys.
- Remove the now-unused `PolicyFactory` type alias import.

---

### 4. `gateway/gateway-runtime/python-executor/executor/policy_cache.py`

**Delete this entire file.** It is no longer needed.

Checklist before deleting:
- Ensure no other file imports from it except `server.py` (already verified — only `server.py` imports `PolicyCache`).
- The `HealthCheck` RPC in `server.py` calls `self._cache.get_stats()` — this needs updating (see server.py changes below).

---

### 5. `gateway/gateway-runtime/python-executor/executor/server.py`

This is the main change. The servicer currently does:
1. Extract params from proto
2. Call `self._cache.get_or_create(name, version, metadata, params)` → get a `Policy` instance
3. Call `policy.on_request(ctx, params)` or `policy.on_response(ctx, params)`

It should now do:
1. Extract params from proto
2. Call `self._loader.get_policy_functions(name, version)` → get `(on_request_fn, on_response_fn)`
3. Call `on_request_fn(ctx, params)` or `on_response_fn(ctx, params)`

**Detailed changes:**

- **Remove** `from executor.policy_cache import PolicyCache` import.
- **Remove** `from sdk.policy import Policy` import (no longer needed).
- **Change `PythonExecutorServicer.__init__`**:
  - Remove `policy_cache` parameter.
  - Remove `self._cache = policy_cache`.
  - Keep `self._loader`, `self._translator`, `self._executor`.
  - New signature: `def __init__(self, policy_loader: PolicyLoader, worker_count: int = 4)`
- **Change `_execute_policy_inner()`** — this is the core change:

  Replace:
  ```python
  metadata = self._translator.to_python_policy_metadata(request.policy_metadata)
  params = self._translator.struct_to_dict(request.params)

  policy = self._cache.get_or_create(
      request.policy_name,
      request.policy_version,
      metadata,
      params
  )
  ```

  With:
  ```python
  params = self._translator.struct_to_dict(request.params)
  on_request_fn, on_response_fn = self._loader.get_policy_functions(
      request.policy_name,
      request.policy_version,
  )
  ```

  Then replace:
  ```python
  # on_request phase
  action = policy.on_request(req_ctx, params)
  ```
  With:
  ```python
  action = on_request_fn(req_ctx, params)
  ```

  And replace:
  ```python
  # on_response phase
  action = policy.on_response(resp_ctx, params)
  ```
  With:
  ```python
  action = on_response_fn(resp_ctx, params)
  ```

  **Note**: The `metadata` translation (`to_python_policy_metadata`) is no longer needed since we don't construct instances. However, if a policy function needs metadata, it's available in `request.shared_context` which is already passed via `RequestContext.shared`. If any policy needs route-specific metadata, it can access it through `ctx.shared`. So no information is lost. Simply remove the `metadata = self._translator.to_python_policy_metadata(...)` line.

- **Change `_error_response()`**: The error_type detection for "init_error" can be simplified — there is no more init phase. All errors are "execution_error". Keep the existing logic but remove the "init_error" branch:
  ```python
  error_type = "execution_error"
  ```

- **Change `HealthCheck()`** method:
  Current:
  ```python
  stats = self._cache.get_stats()
  return proto.HealthCheckResponse(
      ready=True,
      loaded_policies=stats["valid_entries"]
  )
  ```
  New:
  ```python
  loaded = self._loader.get_loaded_policy_count()
  return proto.HealthCheckResponse(
      ready=True,
      loaded_policies=loaded
  )
  ```
  This requires adding `get_loaded_policy_count()` to `PolicyLoader` (see policy_loader changes).

- **Change `PythonExecutorServer` class:**
  - Remove `self._cache = PolicyCache(self._loader)` from `__init__`.
  - In `start()`, change the `PythonExecutorServicer` constructor call:
    ```python
    # Old
    servicer = PythonExecutorServicer(self._loader, self._cache, self.worker_count)
    # New
    servicer = PythonExecutorServicer(self._loader, self.worker_count)
    ```

---

### 6. `gateway/gateway-runtime/python-executor/main.py`

**No changes needed** — `main.py` creates `PythonExecutorServer(SOCKET_PATH, WORKER_COUNT)` and the server internally creates the loader. The cache was created inside `PythonExecutorServer`, so removing it there covers this.

Double-check: main.py does NOT import `PolicyCache` directly. ✅

---

### 7. `gateway/gateway-builder/internal/policyengine/generator.go`

**What to change:**

The `generatePythonRegistry()` function generates `python_policy_registry.py` which currently maps `"name:version"` → `"policies.module_name.policy"` (module path where `get_policy` lives).

For the stateless model, the registry format stays the same — it still maps to the module path. The difference is that `PolicyLoader` will now look for `on_request`/`on_response` functions in that module instead of `get_policy`. **No change needed in the generator** because the registry just points to the module, not to a specific function name.

However, to be explicit and future-proof, consider updating the generated registry to include the function names. But this is **optional** — the simpler approach is to keep it as-is and let `PolicyLoader` handle the convention.

**Verdict: No changes needed in generator.go.** The module path format is sufficient.

---

### 8. `gateway/sample-policies/add-python-header/policy.py`

**Convert from class-based to function-based.**

Current:
```python
from sdk.policy import (
    Policy, PolicyMetadata, RequestContext, ResponseContext,
    UpstreamRequestModifications, RequestAction, ResponseAction,
)
from typing import Dict, Any

class AddPythonHeaderPolicy(Policy):
    def __init__(self, metadata: PolicyMetadata, params: Dict[str, Any]):
        super().__init__(metadata, params)
        self.header_name = params.get("headerName", "X-Python-Policy")
        self.header_value = params.get("headerValue", "hello-from-python")

    def on_request(self, ctx: RequestContext, params: Dict[str, Any]) -> RequestAction:
        return UpstreamRequestModifications(
            set_headers={self.header_name: self.header_value}
        )

    def on_response(self, ctx: ResponseContext, params: Dict[str, Any]) -> ResponseAction:
        return None

def get_policy(metadata: PolicyMetadata, params: dict) -> Policy:
    return AddPythonHeaderPolicy(metadata, params)
```

New:
```python
from sdk.policy import (
    RequestContext, ResponseContext,
    UpstreamRequestModifications, RequestAction, ResponseAction,
)
from typing import Dict, Any


def on_request(ctx: RequestContext, params: Dict[str, Any]) -> RequestAction:
    """Add the configured header to the request."""
    header_name = params.get("headerName", "X-Python-Policy")
    header_value = params.get("headerValue", "hello-from-python")
    return UpstreamRequestModifications(
        set_headers={header_name: header_value}
    )


def on_response(ctx: ResponseContext, params: Dict[str, Any]) -> ResponseAction:
    """Pass-through — no modifications to response."""
    return None
```

**Key differences:**
- No class, no `__init__`, no `self`.
- No `get_policy` factory.
- `params.get()` is called directly in the function — no caching in `self`. For a trivial dict lookup this is negligible.
- Imports are cleaner — no `Policy`, `PolicyMetadata` needed.

---

### 9. `gateway/docs/python-policy-support.md`

**Update:**
- In the "Files Created" table for `executor/policy_cache.py` — mark as **deleted** or remove the row.
- Update description of `executor/server.py` — "dispatches to policy functions" instead of "dispatches to policies via `ThreadPoolExecutor`".
- Update description of `executor/policy_loader.py` — "loads `on_request`/`on_response` functions" instead of "loads policies from generated `python_policy_registry.py` using `importlib`".
- Update the description of `sdk/policy.py` — mention function-based approach is primary, class-based is deprecated.
- Update description of sample policy — "exports `on_request()`/`on_response()` functions" instead of class+factory.
- In the architecture text, remove mention of "lazy content-addressed cache" and "policy instances".

---

## Execution Order

The changes have dependencies. Execute in this order:

1. **`sdk/policy.py`** + **`sdk/__init__.py`** — add new type aliases (non-breaking, additive)
2. **`executor/policy_loader.py`** — change to load functions + add backward compat wrapper
3. **`executor/server.py`** — remove cache, call functions directly
4. **`executor/policy_cache.py`** — delete the file
5. **`sample-policies/add-python-header/policy.py`** — convert to function-based
6. **`docs/python-policy-support.md`** — update documentation

---

## What NOT to Change

These files require **zero modifications**:

| File | Reason |
|------|--------|
| `proto/python_executor.proto` | Proto contract is unchanged — still sends `policy_name`, `params`, `phase`, `context` |
| `proto/python_executor_pb2.py` | Generated from proto — no change |
| `proto/python_executor_pb2_grpc.py` | Generated from proto — no change |
| `executor/translator.py` | Still converts proto ↔ Python SDK types — those types haven't changed |
| `executor/metrics.py` | Still tracks latency/concurrency — nothing policy-instance-specific |
| `main.py` | Only creates `PythonExecutorServer` — the cache removal is internal to it |
| All Go code (`pythonbridge/`, `factory.go`, `client.go`, `bridge.go`, `translator.go`) | Go doesn't know or care if Python uses classes or functions. The proto request/response contract is identical. |
| `gateway-builder/internal/policyengine/generator.go` | Registry format stays the same (module path). Loader handles the convention change. |
| `docker-entrypoint.sh` | No changes — `PYTHON_POLICY_WORKERS` env var still controls `ThreadPoolExecutor` size |
| `Dockerfile` | No changes |

---

## Verification

### Unit Test

Create a simple test that verifies the stateless flow works:

```python
# Test: policy_loader loads on_request/on_response functions from a module
# Test: server._execute_policy_inner() calls the function directly with (ctx, params)
# Test: HealthCheck returns correct loaded count
```

### Manual E2E Test

1. Rebuild: `cd gateway && make build-local-gateway-runtime`
2. Start: `cd gateway && docker compose up -d`
3. Deploy a test API with `add-python-header` policy (see existing benchmark setup from `RESULTS.md`)
4. Send a request: `curl -v http://localhost:8080/<api-path>/echo`
5. Verify the `X-Python-Policy: hello-from-python` header is present in the upstream request
6. Check logs: `docker compose logs gateway-runtime | grep pye` — should show function loading, no cache references

### Integration Tests

No existing Python policy integration tests exist in `gateway/it/`. If integration tests are added later, they will work identically since the gRPC contract and external behavior are unchanged.

---

## Summary

| Metric | Before | After |
|--------|--------|-------|
| Files modified | — | 5 (`sdk/policy.py`, `sdk/__init__.py`, `policy_loader.py`, `server.py`, sample `policy.py`) |
| Files deleted | — | 1 (`policy_cache.py`) |
| Files updated (docs) | — | 1 (`python-policy-support.md`) |
| Lines added (est.) | — | ~30 |
| Lines removed (est.) | — | ~150 (entire cache file + class boilerplate) |
| Go changes | — | 0 |
| Proto changes | — | 0 |
| Threading model | Same | Same (`ThreadPoolExecutor` + asyncio) |
| Backward compat | — | Old class-based policies work via deprecation wrapper in loader |
| Risk | — | Low — the data flow (proto → function → proto) is identical, only the dispatch mechanism changes |
