# Python Policy Support in the Gateway

## How It Works (End-to-End)

```
┌─────────────────────────────────────────────────────────────┐
│                    Gateway Runtime Container                │
│                                                             │
│  ┌─────────┐    ┌──────────────┐    ┌──────────────────┐   │
│  │  Envoy  │───▶│ Policy Engine│───▶│ Python Executor  │   │
│  │ (Router)│    │   (Go)       │    │   (Python 3)     │   │
│  └─────────┘    └──────┬───────┘    └────────┬─────────┘   │
│                        │   gRPC bidi stream   │             │
│                        │   over Unix Socket   │             │
│                        └──────────────────────┘             │
│                                                             │
│  Process management: tini → Python Executor → Policy Engine │
│                      → Envoy (sequential startup)           │
└─────────────────────────────────────────────────────────────┘
```

### Request Flow

1. **Envoy** receives an HTTP request, sends it to the **Policy Engine** via ext_proc
2. Policy Engine runs the policy chain for that route — a mix of Go and Python policies
3. For **Go policies**: executed directly in-process
4. For **Python policies**: the `PythonBridge` sends a gRPC request over UDS to the **Python Executor**, which runs the policy's `on_request()`/`on_response()` functions, and returns the action (add headers, modify body, reject, etc.)
5. Result is merged back into the policy chain seamlessly — Go policies don't know Python was involved

### Build Flow

1. `gateway-builder` reads `build.yaml`, discovers policies (Go or Python based on `runtime: python` in `policy-definition.yaml`)
2. For Python policies: copies the executor source + policy sources into the build output, generates `python_policy_registry.py` (maps `"name:version"` → module path), merges `requirements.txt`
3. Generates `plugin_registry.go` which registers Go policies directly and Python policies via `BridgeFactory`
4. Dockerfile installs Python deps using the runtime's own `python3` and copies everything into the final image

## Is It Modular? (Can you run without Python?)

**Yes, fully modular.** If no Python policies are in `build.yaml`:

- `generatePythonExecutorBase()` creates an empty scaffold (so Docker `COPY` doesn't fail)
- No `BridgeFactory` registrations in `plugin_registry.go`
- The entrypoint checks `if [ -f /app/python-executor/main.py ]` — if no Python policies, the registry file won't exist, so the Python Executor **never starts**
- Gateway runs with only Go policies as before

The only overhead for a Go-only build is `python3` being installed in the runtime image (~30MB). This can be made conditional in a future optimization.

## Files Created

### Proto Definition
| File | Purpose |
|------|---------|
| `gateway-runtime/proto/python_executor.proto` | gRPC contract: `ExecuteStream` (bidi), `HealthCheck` |

### Go — Python Bridge (`policy-engine/internal/pythonbridge/`)
| File | Purpose |
|------|---------|
| `bridge.go` | `PythonBridge` struct implementing `policy.Policy` — delegates to Python via gRPC |
| `client.go` | `StreamManager` — singleton gRPC client, persistent bidi stream, request-ID correlation |
| `factory.go` | `BridgeFactory` — creates `PythonBridge` instances, registered as `PolicyFactory` |
| `translator.go` | Converts proto responses → Go SDK action types |
| `proto/python_executor.pb.go` | Generated Go protobuf types |
| `proto/python_executor_grpc.pb.go` | Generated Go gRPC client stubs |

### Python Executor (`gateway-runtime/python-executor/`)
| File | Purpose |
|------|---------|
| `main.py` | Entry point — asyncio event loop, signal handling, logging setup |
| `executor/server.py` | gRPC servicer — handles `ExecuteStream`, dispatches to policy functions via `ThreadPoolExecutor` |
| `executor/policy_loader.py` | Loads `on_request`/`on_response` functions from generated `python_policy_registry.py` using `importlib` |
| ~~`executor/policy_cache.py`~~ | ~~Lazy content-addressed cache — key is `(name, version, hash(params))`~~ **(Removed — now stateless)** |
| `executor/translator.py` | Converts proto messages ↔ Python SDK types |
| `sdk/policy.py` | Python Policy SDK — `Policy` ABC (deprecated), context/action dataclasses, function type aliases |
| `requirements.txt` | Base deps: `grpcio`, `protobuf` |
| `proto/python_executor_pb2.py` | Generated Python protobuf types |
| `proto/python_executor_pb2_grpc.py` | Generated Python gRPC stubs |

### Sample Python Policy (`sample-policies/add-python-header/`)
| File | Purpose |
|------|---------|
| `policy-definition.yaml` | Policy metadata: name, version, `runtime: python`, `processingMode`, params schema |
| `policy.py` | Implementation: exports `on_request()`/`on_response()` functions |
| `requirements.txt` | Policy-specific deps (empty for this simple policy) |

### Controller Policy Definition
| File | Purpose |
|------|---------|
| `gateway-controller/default-policies/add-python-header.yaml` | So the controller knows the policy exists and can validate API configs |

## Files Modified

### Gateway Builder
| File | Change |
|------|--------|
| `pkg/types/manifest.go` | Added `Pythonmodule` field to `BuildEntry` |
| `pkg/types/policy.go` | Added `Runtime`, `PythonSourceDir`, `ProcessingMode` to `DiscoveredPolicy` |
| `internal/discovery/manifest.go` | Added `discoverPythonPolicy()`, `parseProcessingMode()`, `filePath` runtime detection |
| `internal/discovery/policy.go` | Added `ValidatePythonDirectoryStructure()`, `CollectPythonSourceFiles()` |
| `internal/discovery/python_module.go` | **New file** — fetches remote Python policies via GitHub tarball |
| `internal/policyengine/generator.go` | Added `generatePythonExecutorBase()`, `GeneratePythonArtifacts()`, `generatePythonRegistry()`, `mergeRequirements()` |
| `internal/policyengine/registry.go` | Added `Runtime`/`ProcessingMode` to `PolicyImport`, template data for Python |
| `templates/plugin_registry.go.tmpl` | Added Python policy registration block using `BridgeFactory` |

### SDK
| File | Change |
|------|--------|
| `sdk/gateway/policy/v1alpha/definition.go` | Added `Runtime`, `ProcessingModeConfig` to `PolicyDefinition` |

### Gateway Runtime
| File | Change |
|------|--------|
| `Dockerfile` | Added `python3`/`pip3` install, copies executor, pip-installs deps in runtime stage |
| `docker-entrypoint.sh` | Added Python Executor startup, UDS socket wait, `--py.*` arg forwarding, process monitoring |

### Build Config
| File | Change |
|------|--------|
| `build.yaml` | Added `add-python-header` entry with `filePath` |

## Writing a Python Policy

Python policies are **stateless functions**, not classes. Each policy module exports two functions:

```python
from typing import Any, Dict
from sdk.policy import RequestContext, ResponseContext, RequestAction, ResponseAction

def on_request(ctx: RequestContext, params: Dict[str, Any]) -> RequestAction:
    """Process request phase.
    
    Args:
        ctx: Request context with headers, body, path, method, shared metadata.
        params: Policy configuration parameters from policy-definition.yaml.
    
    Returns:
        None for pass-through, UpstreamRequestModifications for changes,
        or ImmediateResponse to short-circuit.
    """
    # Example: add a header
    from sdk.policy import UpstreamRequestModifications
    return UpstreamRequestModifications(set_headers={"X-Custom": "value"})

def on_response(ctx: ResponseContext, params: Dict[str, Any]) -> ResponseAction:
    """Process response phase.
    
    Args:
        ctx: Response context with request data, response headers/body/status, shared metadata.
        params: Policy configuration parameters.
    
    Returns:
        None for pass-through or UpstreamResponseModifications for changes.
    """
    return None  # Pass-through
```

### Key differences from class-based (deprecated) approach:
- **No class, no `__init__`, no `self`** — policies are pure functions
- **No `get_policy` factory** — functions are called directly by the executor
- **Parameters passed per-request** — read from `params` dict in each function call
- **Thread-safe by design** — no shared mutable state between requests

See `sample-policies/add-python-header/policy.py` for a complete example.

## Bug Fixes Applied During Bring-up

| Bug | Fix |
|-----|-----|
| `grpcio` C extension mismatch — `python:3.11` build stage vs `python3.10` runtime | Removed separate `python-deps` Docker stage; pip-install in runtime stage using its own `python3` |
| `import python_executor_pb2` failing — absolute import in generated gRPC stub | Changed to `from proto import python_executor_pb2` |
| Missing `proto/__init__.py` | Created it so `proto/` is a proper Python package |
| `proto.Struct` type annotation — references wrong module | Removed incorrect type annotation on `_dict_to_struct()` |
| `struct_to_dict()` returning protobuf `Value` objects instead of native Python types | Replaced with `_proto_value_to_python()` that uses `WhichOneof('kind')` to unwrap properly |
| `filePath:` Python policies routed to Go discovery | Added runtime detection: reads `policy-definition.yaml` to check `runtime` before dispatching |
| Docker build failing without Python policies | Added `generatePythonExecutorBase()` that always creates the output directory |

## Stateless Python Executor (2025-02)

The Python Executor was refactored to be **stateless**:

| Before | After |
|--------|-------|
| `Policy` class with `__init__` and `get_policy` factory | `on_request()`/`on_response()` functions exported directly |
| `PolicyCache` creating instances per `(name, version, params_hash)` | No cache — functions called directly with params |
| Risk of shared mutable state, threading issues | Thread-safe by design — no shared state |
| Class boilerplate required | Simple functions, minimal code |

The Go side requires **zero changes** — the gRPC contract is identical. Only the Python side dispatch mechanism changed.

## Verified Working

Tested with a **Go → Python → Go** policy chain:
- `log-message` (Go) → `add-python-header` (Python) → `add-headers` (Go)
- Both `X-Python-Policy` and `X-Go-Policy` headers appeared in the upstream request
- All three processes (Python Executor, Policy Engine, Envoy) running stable in the container
