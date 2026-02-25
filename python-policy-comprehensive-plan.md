# Python Policy Support — Comprehensive Implementation Plan

> **Audience**: A coding agent with zero prior context about this codebase.
> **Goal**: Add Python policy support to the WSO2 API Platform Gateway.
> This document is fully self-contained. Every architecture detail, file reference, code pattern, and design decision is explained from scratch.

---

## Table of Contents

1. [Current Architecture Overview](#1-current-architecture-overview)
2. [How Go Policies Work Today (End-to-End)](#2-how-go-policies-work-today-end-to-end)
3. [What Python Policy Support Means](#3-what-python-policy-support-means)
4. [Architecture Decision: Sidecar via gRPC over UDS](#4-architecture-decision-sidecar-via-grpc-over-uds)
5. [Proto Definition (Go ↔ Python)](#5-proto-definition-go--python)
6. [Go-Side Changes: PythonBridge Policy](#6-go-side-changes-pythonbridge-policy)
7. [Python-Side: Executor Service](#7-python-side-executor-service)
8. [Python SDK Design](#8-python-sdk-design)
9. [Build System Changes](#9-build-system-changes)
10. [Dockerfile Changes](#10-dockerfile-changes)
11. [Entrypoint Script Changes](#11-entrypoint-script-changes)
12. [Caching Strategy](#12-caching-strategy)
13. [Error Handling](#13-error-handling)
14. [Metrics and Observability](#14-metrics-and-observability)
15. [Implementation Phases](#15-implementation-phases)
16. [File-by-File Change Inventory](#16-file-by-file-change-inventory)
17. [Testing Strategy](#17-testing-strategy)
18. [Decisions Log](#18-decisions-log)

---

## 1. Current Architecture Overview

### 1.1 Two-Container Model

The WSO2 API Gateway runs as **two separate containers** (typically in the same Kubernetes pod or Docker Compose network):

| Container | Name | Purpose |
|-----------|------|---------|
| **Gateway Controller** | `gateway-controller` | Go service. Stores API definitions in SQLite. Exposes a REST API for CRUD operations on APIs. Serves xDS streams to the gateway-runtime on two ports: port 18000 for Envoy route/cluster/listener configs, port 18001 for policy chain configs (which policies to run per route, with what parameters). |
| **Gateway Runtime** | `gateway-runtime` | Single container running **two processes** under `tini` (PID 1): (1) **Go Policy Engine** — a gRPC ext_proc server listening on a Unix Domain Socket (UDS) at `/var/run/api-platform/policy-engine.sock`, and (2) **Envoy Router** — the Envoy proxy (ports 8080/8443 for traffic, 9901 admin). |

The `docker-entrypoint.sh` script in the gateway-runtime container:
- Starts the Go Policy Engine first, waits for the UDS socket to appear
- Then starts Envoy
- Monitors both — if either dies, it kills the other and exits

### 1.2 Request Flow

```
Client → Envoy (port 8080/8443)
           ↓ ext_proc gRPC (bidirectional streaming over UDS)
         Go Policy Engine
           ↓ returns modifications/immediate response
         Envoy → Upstream Service
```

Every HTTP request through Envoy triggers one bidirectional gRPC stream to the Policy Engine. The stream carries 4 possible phases:
1. **Request Headers** — always sent first
2. **Request Body** — sent only if any policy in the chain needs the request body
3. **Response Headers** — sent after upstream responds
4. **Response Body** — sent only if any policy needs the response body

The Go Policy Engine decides at request-headers time which subsequent phases Envoy should send, via a `ModeOverride` in the response. This is a performance optimization — if no policy needs the request body, Envoy skips buffering it entirely.

### 1.3 Key Source Files (Current)

All paths below are relative to the repository root.

| File | What It Does |
|------|-------------|
| `gateway/gateway-runtime/Dockerfile` | 3-stage Docker build. Stage 1: compiles the Gateway Builder binary. Stage 2: runs Gateway Builder to generate code and compile the Policy Engine binary. Stage 3: FROM envoyproxy/envoy, installs tini, copies the compiled Policy Engine binary and configs. |
| `gateway/gateway-runtime/docker-entrypoint.sh` | Bash script (220 lines). Manages 2 processes (Policy Engine + Envoy). Parses `--pol.*` and `--rtr.*` prefixed args. Sets up env vars. Handles SIGTERM graceful shutdown. |
| `gateway/build.yaml` | Build manifest. Lists ~36 Go policies with `gomodule:` entries pointing to `github.com/wso2/gateway-controllers/policies/<name>@v0`. |
| `gateway/gateway-builder/cmd/builder/main.go` | Gateway Builder CLI (341 lines). Runs 6 phases: Discovery → Validation → Code Generation → Compilation → Dockerfile Generation → Build Info. |
| `gateway/gateway-builder/pkg/types/manifest.go` | Defines `BuildFile` and `BuildEntry` structs parsed from `build.yaml`. |
| `gateway/gateway-builder/pkg/types/policy.go` | Defines `DiscoveredPolicy` struct with all metadata about a discovered policy. |
| `gateway/gateway-builder/internal/discovery/manifest.go` | Loads `build.yaml`, validates it, discovers policies by resolving Go modules or local file paths. |
| `gateway/gateway-builder/internal/discovery/policy.go` | Parses `policy-definition.yaml`, validates directory structure (requires `go.mod` + `.go` files). |
| `gateway/gateway-builder/internal/policyengine/generator.go` | Orchestrates code generation: calls `GeneratePluginRegistry()`, `GenerateBuildInfo()`, `UpdateGoMod()`. |
| `gateway/gateway-builder/internal/policyengine/registry.go` | Generates `plugin_registry.go` from a Go template. Creates `PolicyImport` structs with import paths and aliases. |
| `gateway/gateway-builder/templates/plugin_registry.go.tmpl` | Go template that generates an `init()` function which registers each policy with the runtime registry. |
| `gateway/gateway-builder/templates/templates.go` | Embeds all `.tmpl` files via Go's `//go:embed` directives. |
| `sdk/gateway/policy/v1alpha/interface.go` | Defines the `Policy` interface: `Mode()`, `OnRequest()`, `OnResponse()`. Also `PolicyFactory`, `ProcessingMode`, `HeaderProcessingMode`, `BodyProcessingMode`. |
| `sdk/gateway/policy/v1alpha/context.go` | Defines `SharedContext`, `RequestContext`, `ResponseContext` structs. |
| `sdk/gateway/policy/v1alpha/action.go` | Defines action types: `UpstreamRequestModifications`, `ImmediateResponse`, `UpstreamResponseModifications`. |
| `sdk/gateway/policy/v1alpha/definition.go` | Defines `PolicyDefinition` (name, version, parameters schema, systemParameters schema), `PolicySpec`, `PolicyParameters`. |
| `gateway/gateway-runtime/policy-engine/internal/registry/registry.go` | Runtime singleton `PolicyRegistry` with `Register()`, `CreateInstance()`. Merges system params + user params, resolves `${config}` references, calls factory. |
| `gateway/gateway-runtime/policy-engine/internal/registry/chain.go` | `PolicyChain` struct: ordered `Policies[]`, `PolicySpecs[]`, `RequiresRequestBody`, `RequiresResponseBody`, `HasExecutionConditions` flags. |
| `gateway/gateway-runtime/policy-engine/internal/kernel/extproc.go` | `ExternalProcessorServer` implementing Envoy's ext_proc gRPC service. `Process()` method handles bidirectional stream. Routes phases to `handleProcessingPhase()`. |
| `gateway/gateway-runtime/policy-engine/internal/kernel/execution_context.go` | `PolicyExecutionContext` — per-request lifecycle. Methods: `processRequestHeaders()`, `processRequestBody()`, `processResponseHeaders()`, `processResponseBody()`. Also `getModeOverride()` which computes what Envoy should buffer. |
| `gateway/gateway-runtime/policy-engine/internal/kernel/mapper.go` | `Kernel` struct with `Routes map[string]*PolicyChain`. xDS updates replace routes atomically via `ApplyWholeRoutes()`. |
| `gateway/gateway-runtime/policy-engine/internal/executor/chain.go` | `ChainExecutor` with `ExecuteRequestPolicies()` and `ExecuteResponsePolicies()`. Iterates policies in order (request) or reverse order (response), calls `OnRequest()`/`OnResponse()`, applies modifications in-place, handles short-circuit via `StopExecution()`. |

---

## 2. How Go Policies Work Today (End-to-End)

### 2.1 Build Time (Gateway Builder)

1. **Discovery**: The builder reads `build.yaml` which lists policies with `gomodule:` entries (or `filePath:` for local). For each entry, it:
   - Downloads the Go module via `go mod download -json`
   - Reads `policy-definition.yaml` from the module to get name, version, parameter schemas, systemParameters schemas
   - Validates the directory has `go.mod` and `.go` files

2. **Code Generation**: From the discovered policies, the builder generates:
   - `plugin_registry.go` — An auto-generated file in `cmd/policy-engine/` that imports each policy package and registers it via `registry.GetRegistry().Register(policyDef, pkg.GetPolicy)` in an `init()` function.
   - `build_info.go` — Build metadata (timestamp, version, policy list).
   - Updates `go.mod` with `go get` for remote modules and `replace` directives for local ones.

3. **Compilation**: The builder compiles all of the policy engine source code (including the generated files) into a single Go binary.

4. **Dockerfile Generation**: Generates a Dockerfile that layers the compiled binary onto the base gateway-runtime image.

### 2.2 Runtime (Request Processing)

1. **Startup**: The Policy Engine binary starts. The generated `init()` function in `plugin_registry.go` runs, registering all policies into the global `PolicyRegistry` singleton.

2. **xDS Configuration**: The Policy Engine connects to the Gateway Controller's xDS stream (port 18001). It receives route configurations specifying which policies to execute for each route, with what parameters. For each route, it calls `PolicyRegistry.CreateInstance()` which:
   - Looks up the policy by `name:majorVersion` key
   - Extracts `systemParameters` from the PolicyDefinition
   - Resolves `${config.xxx}` references in systemParameters against the gateway config file
   - Merges resolved systemParameters with user-defined parameters (user params override)
   - Calls the `PolicyFactory` function (i.e., `GetPolicy(metadata, mergedParams)`) to create an instance
   - The instance and its parameters are stored in a `PolicyChain`

3. **Route Mapping**: The Kernel stores `map[string]*PolicyChain` (route key → chain). When routes change, the entire map is atomically replaced via `ApplyWholeRoutes()`.

4. **Request Execution**:
   - Envoy sends request headers on the ext_proc stream
   - The Policy Engine looks up the route key from Envoy metadata, gets the PolicyChain
   - It computes `getModeOverride()` — if any policy in the chain has `BodyProcessingMode == BUFFER`, the chain sets `RequiresRequestBody = true`, and the mode tells Envoy to buffer the body
   - If body is NOT required: policies execute immediately with headers only
   - If body IS required: the engine returns an empty response for headers, waits for the body phase, then executes all policies with both headers and body
   - `ChainExecutor.ExecuteRequestPolicies()` iterates policies in order, calling `pol.OnRequest(ctx, spec.Parameters.Raw)` for each
   - Each policy returns a `RequestAction`: either `UpstreamRequestModifications` (continue, with header/body/path changes applied in-place to the context) or `ImmediateResponse` (short-circuit the chain, return error/redirect to client)
   - The final result is translated to ext_proc response and sent back to Envoy

5. **Response Execution**:
   - Same pattern, but policies execute in **reverse order** (`for i := len(policyList)-1; i >= 0; i--`)
   - Actions are `UpstreamResponseModifications` (modify headers/body/status)

### 2.3 Policy Interface (Go)

Every Go policy must implement:

```go
type Policy interface {
    Mode() ProcessingMode      // Declares what phases this policy needs
    OnRequest(ctx *RequestContext, params map[string]interface{}) RequestAction
    OnResponse(ctx *ResponseContext, params map[string]interface{}) ResponseAction
}
```

And export a factory function:

```go
func GetPolicy(metadata PolicyMetadata, params map[string]interface{}) (Policy, error)
```

The `ProcessingMode` struct declares what the policy needs:

```go
type ProcessingMode struct {
    RequestHeaderMode  HeaderProcessingMode  // SKIP or PROCESS
    RequestBodyMode    BodyProcessingMode    // SKIP, BUFFER, or STREAM
    ResponseHeaderMode HeaderProcessingMode  // SKIP or PROCESS
    ResponseBodyMode   BodyProcessingMode    // SKIP, BUFFER, or STREAM
}
```

### 2.4 policy-definition.yaml

Every policy (Go or Python) must have a `policy-definition.yaml`:

```yaml
name: my-policy
version: v1.0.0
description: What this policy does

parameters:          # User-defined params (set in API config by API developers)
  type: object
  properties:
    someParam:
      type: string
      default: "hello"

systemParameters:    # Admin-defined params (gateway config, resolved by gateway at startup)
  type: object
  properties:
    someSecret:
      type: string
      "wso2/defaultValue": "${config.policy_configurations.mypolicy_v010.somesecret}"
```

System parameters use `"wso2/defaultValue"` to reference paths in the gateway's config file. The Go Policy Engine resolves these at startup time, so **policies never see unresolved `${config}` strings** — they receive already-resolved values.

---

## 3. What Python Policy Support Means

We want users to write policies in Python instead of (or in addition to) Go. A Python policy should:
- Have the same `policy-definition.yaml` format
- Implement the same conceptual interface: `mode()`, `on_request()`, `on_response()`
- Be declared in `build.yaml` alongside Go policies
- Be compiled into the Docker image at build time
- Execute at runtime alongside Go policies in the same chain

**The Go Policy Engine remains the orchestrator.** It still handles Envoy's ext_proc stream, manages policy chains, resolves system parameters, and applies modifications. But when a policy is written in Python, the Go engine delegates execution to a **Python sidecar process** running inside the same container.

---

## 4. Architecture Decision: Sidecar via gRPC over UDS

### 4.1 Why gRPC over UDS

- **Language-agnostic**: Protocol Buffers provide a clean contract between Go and Python
- **Low latency**: Unix Domain Sockets avoid TCP overhead. Benchmarks show ~50-100μs per call
- **Bidirectional streaming**: A single stream per Go↔Python connection avoids per-call overhead
- **Schema evolution**: Protobuf supports backward-compatible field additions
- **Alternative considered**: Embedding Python via CGO (rejected: complexity, GIL issues, crash propagation, no clean isolation)

### 4.2 Connection Design

```
gateway-runtime container (3 processes):
┌──────────────────────────────────────────────────────────┐
│  tini (PID 1)                                            │
│  ├── Go Policy Engine (ext_proc on UDS .sock)            │
│  │     └── PythonBridge policies ──gRPC──┐               │
│  ├── Python Executor (gRPC server)  ◄────┘               │
│  │     UDS: /var/run/api-platform/python-executor.sock   │
│  └── Envoy (ports 8080/8443)                             │
└──────────────────────────────────────────────────────────┘
```

The Go Policy Engine is the **gRPC client**. It connects to the Python Executor's UDS socket. Communication uses **bidirectional streaming** — one persistent stream is opened at startup and reused for all policy executions. Each request/response is multiplexed over this single stream using request IDs.

### 4.3 Process Lifecycle

1. Entrypoint starts Python Executor first
2. Waits for `/var/run/api-platform/python-executor.sock` to appear
3. Starts Go Policy Engine (which connects to Python's socket as a client)
4. Waits for `/var/run/api-platform/policy-engine.sock` to appear
5. Starts Envoy (which connects to Policy Engine's socket)
6. Monitors all 3 processes — if any dies, all are terminated

---

## 5. Proto Definition (Go ↔ Python)

Create file: `gateway/gateway-runtime/proto/python_executor.proto`

```protobuf
syntax = "proto3";

package wso2.gateway.python.v1;

option go_package = "github.com/wso2/api-platform/gateway/gateway-runtime/policy-engine/internal/pythonbridge/proto";

import "google/protobuf/struct.proto";

// PythonExecutorService defines the gRPC contract between Go PE and the Python process.
// The Python process is the gRPC SERVER, Go PE is the CLIENT.
service PythonExecutorService {
  // Bidirectional stream for executing policies.
  // Go sends ExecutionRequest, Python responds with ExecutionResponse.
  // Each request has a unique request_id that the response must echo back.
  rpc ExecuteStream (stream ExecutionRequest) returns (stream ExecutionResponse);

  // Health check for readiness.
  rpc HealthCheck (HealthCheckRequest) returns (HealthCheckResponse);
}

// ────────────────────── Request / Response ──────────────────────

message ExecutionRequest {
  // Unique ID per call so responses can be correlated on the single stream.
  string request_id = 1;

  // Policy to execute (name:version format used for cache lookup, e.g., "my-policy:v1")
  string policy_name = 2;
  string policy_version = 3;

  // Phase: "on_request" or "on_response"
  string phase = 4;

  // Merged parameters (system + user) for this policy instance.
  // The Go side resolves ${config} references in systemParameters and merges
  // them with user parameters before sending. Python never sees raw ${config} strings.
  google.protobuf.Struct params = 5;

  // The request or response context data
  oneof context {
    RequestContext request_context = 6;
    ResponseContext response_context = 7;
  }

  // Shared context (metadata, API info, auth context)
  SharedContext shared_context = 8;

  // Policy metadata (route info, API info) for factory creation
  PolicyMetadata policy_metadata = 9;
}

message ExecutionResponse {
  // Must match request_id from the corresponding ExecutionRequest
  string request_id = 1;

  oneof result {
    RequestActionResult request_result = 2;
    ResponseActionResult response_result = 3;
    ExecutionError error = 4;
  }

  // Updated shared metadata (Python may have mutated it).
  // Go side merges this back into the SharedContext.
  google.protobuf.Struct updated_metadata = 5;
}

// ────────────────────── Context Messages ──────────────────────

message SharedContext {
  string project_id = 1;
  string request_id = 2;
  google.protobuf.Struct metadata = 3;     // Inter-policy communication map
  string api_id = 4;
  string api_name = 5;
  string api_version = 6;
  string api_kind = 7;
  string api_context = 8;
  string operation_path = 9;
  map<string, string> auth_context = 10;
}

message RequestContext {
  map<string, string> headers = 1;
  bytes body = 2;
  bool body_present = 3;
  bool end_of_stream = 4;
  string path = 5;
  string method = 6;
  string authority = 7;
  string scheme = 8;
}

message ResponseContext {
  // Original request data (immutable)
  map<string, string> request_headers = 1;
  bytes request_body = 2;
  string request_path = 3;
  string request_method = 4;

  // Response data
  map<string, string> response_headers = 5;
  bytes response_body = 6;
  bool response_body_present = 7;
  int32 response_status = 8;
}

message PolicyMetadata {
  string route_name = 1;
  string api_id = 2;
  string api_name = 3;
  string api_version = 4;
  string attached_to = 5;   // "api" or "route"
}

// ────────────────────── Action Results ──────────────────────

message RequestActionResult {
  oneof action {
    UpstreamRequestModifications continue_request = 1;
    ImmediateResponseAction immediate_response = 2;
  }
}

message ResponseActionResult {
  oneof action {
    UpstreamResponseModifications continue_response = 1;
  }
}

message UpstreamRequestModifications {
  map<string, string> set_headers = 1;
  repeated string remove_headers = 2;
  map<string, StringList> append_headers = 3;
  bytes body = 4;
  bool body_present = 5;              // false means no body change, true means use body field (even if empty)
  string path = 6;
  bool path_present = 7;
  string method = 8;
  bool method_present = 9;
  google.protobuf.Struct analytics_metadata = 10;
}

message UpstreamResponseModifications {
  map<string, string> set_headers = 1;
  repeated string remove_headers = 2;
  map<string, StringList> append_headers = 3;
  bytes body = 4;
  bool body_present = 5;
  int32 status_code = 6;
  bool status_code_present = 7;
  google.protobuf.Struct analytics_metadata = 8;
}

message ImmediateResponseAction {
  int32 status_code = 1;
  map<string, string> headers = 2;
  bytes body = 3;
  google.protobuf.Struct analytics_metadata = 4;
}

message ExecutionError {
  string message = 1;
  string policy_name = 2;
  string policy_version = 3;
  string error_type = 4;    // "init_error", "execution_error", "timeout"
}

// ────────────────────── Health Check ──────────────────────

message HealthCheckRequest {}

message HealthCheckResponse {
  bool ready = 1;
  int32 loaded_policies = 2;
}

// ────────────────────── Utility ──────────────────────

message StringList {
  repeated string values = 1;
}
```

### 5.1 Why `google.protobuf.Struct` for params/metadata

Policy parameters come from YAML/JSON and can be arbitrarily nested maps, arrays, strings, numbers, booleans. `google.protobuf.Struct` is the protobuf well-known type designed exactly for this — it maps to Python `dict` and Go `map[string]interface{}` natively.

---

## 6. Go-Side Changes: PythonBridge Policy

### 6.1 Concept

For each Python policy in the build manifest, instead of importing a Go package, the generated `plugin_registry.go` will register a **PythonBridge** — a Go struct that implements the `Policy` interface but delegates `OnRequest()` and `OnResponse()` to the Python Executor via gRPC.

From the Go Policy Engine's perspective, a PythonBridge is just another `Policy`. It sits in the same `PolicyChain`, has the same caching, same mode override, same chain execution. The chain executor calls `bridge.OnRequest(ctx, params)` and gets back a `RequestAction`, without knowing that the execution happened in Python.

### 6.2 New Go Package: `pythonbridge`

Create package: `gateway/gateway-runtime/policy-engine/internal/pythonbridge/`

Files to create:
- `bridge.go` — PythonBridge struct and Policy interface implementation
- `client.go` — gRPC client (StreamManager) that manages the bidirectional stream
- `factory.go` — BridgeFactory that creates PythonBridge instances
- `proto/` — Generated protobuf Go code (from `python_executor.proto`)

#### 6.2.1 `client.go` — StreamManager

```go
// StreamManager manages a persistent bidirectional gRPC stream to the Python Executor.
// It is a singleton created at Policy Engine startup.
//
// Thread safety: Multiple goroutines (one per ext_proc stream) call Execute() concurrently.
// The StreamManager uses a pendingRequests map protected by a mutex to correlate
// request_id → response channel.
//
// Flow:
//   1. Execute() generates a unique request_id, creates a response channel, adds to pendingRequests
//   2. Sends the ExecutionRequest on the stream (protected by a send mutex)
//   3. A background goroutine continuously receives from the stream, looks up request_id
//      in pendingRequests, and sends the response on the channel
//   4. Execute() waits on the channel with a timeout, returns the result

type StreamManager struct {
    conn        *grpc.ClientConn
    stream      proto.PythonExecutorService_ExecuteStreamClient
    sendMu      sync.Mutex                              // Protects stream.Send()
    pendingMu   sync.RWMutex                            // Protects pendingRequests
    pendingReqs map[string]chan *proto.ExecutionResponse  // request_id → response channel
}
```

The StreamManager has:
- `Connect(socketPath string)` — dials the UDS, opens the bidirectional stream, starts receive goroutine
- `Execute(ctx context.Context, req *proto.ExecutionRequest) (*proto.ExecutionResponse, error)` — sends request, waits for response with context deadline
- `Close()` — gracefully closes the stream and connection
- An internal `receiveLoop()` goroutine that reads responses and dispatches them

#### 6.2.2 `bridge.go` — PythonBridge

```go
// PythonBridge implements the policy.Policy interface.
// It delegates execution to the Python Executor via the StreamManager.
//
// Each PythonBridge instance corresponds to one policy-per-route
// (same as Go policies get one instance per route).

type PythonBridge struct {
    policyName    string
    policyVersion string
    mode          policy.ProcessingMode  // Static, from policy-definition.yaml
    metadata      policy.PolicyMetadata
    params        map[string]interface{} // Merged system + user params
    streamManager *StreamManager         // Shared singleton
}

func (b *PythonBridge) Mode() policy.ProcessingMode {
    return b.mode
}

func (b *PythonBridge) OnRequest(ctx *policy.RequestContext, params map[string]interface{}) policy.RequestAction {
    // 1. Build proto.ExecutionRequest from ctx and params
    // 2. Call b.streamManager.Execute(context.Background(), req)
    // 3. Translate proto.ExecutionResponse back to policy.RequestAction
    // 4. Merge updated_metadata back into ctx.SharedContext.Metadata
    // 5. On error: return ImmediateResponse with 500
}

func (b *PythonBridge) OnResponse(ctx *policy.ResponseContext, params map[string]interface{}) policy.ResponseAction {
    // Same pattern as OnRequest but for response phase
}
```

#### 6.2.3 `factory.go` — BridgeFactory

```go
// BridgeFactory creates PythonBridge instances. It is registered as the PolicyFactory
// for Python policies in the generated plugin_registry.go.
//
// BridgeFactory holds:
//   - streamManager: shared StreamManager singleton
//   - mode: ProcessingMode parsed from policy-definition.yaml at build time

type BridgeFactory struct {
    StreamManager *StreamManager
    Mode          policy.ProcessingMode
    PolicyName    string
    PolicyVersion string
}

// GetPolicy creates a PythonBridge instance - conforms to policy.PolicyFactory signature
func (f *BridgeFactory) GetPolicy(metadata policy.PolicyMetadata, params map[string]interface{}) (policy.Policy, error) {
    return &PythonBridge{
        policyName:    f.PolicyName,
        policyVersion: f.PolicyVersion,
        mode:          f.Mode,
        metadata:      metadata,
        params:        params,
        streamManager: f.StreamManager,
    }, nil
}
```

### 6.3 ProcessingMode for Python Policies

In Go, each policy returns `Mode()` dynamically. For Python policies, the `ProcessingMode` is declared **statically** in the `policy-definition.yaml`:

```yaml
name: my-python-policy
version: v1.0.0
description: ...
runtime: python    # NEW FIELD

processingMode:    # NEW FIELD — required for Python policies
  requestHeaderMode: PROCESS
  requestBodyMode: BUFFER
  responseHeaderMode: PROCESS
  responseBodyMode: SKIP

parameters:
  type: object
  properties: { ... }

systemParameters:
  type: object
  properties: { ... }
```

The builder reads `processingMode` from the YAML and bakes it into the BridgeFactory at code generation time. This is necessary because the Go engine needs to know the mode at chain-building time (for the `RequiresRequestBody`/`RequiresResponseBody` flags) without calling Python.

**If `processingMode` is omitted, default to:** `requestHeaderMode: PROCESS, requestBodyMode: SKIP, responseHeaderMode: SKIP, responseBodyMode: SKIP` (headers-only policy).

### 6.4 Changes to Generated `plugin_registry.go`

The template must be enhanced to handle Python policies differently. For Go policies, the current template generates:

```go
import myPolicy "github.com/.../my-policy"
// ...
registry.GetRegistry().Register(policyDef, myPolicy.GetPolicy)
```

For Python policies, instead of importing a Go package, it should register a BridgeFactory:

```go
import "github.com/wso2/api-platform/gateway/gateway-runtime/policy-engine/internal/pythonbridge"
// ...
bridgeFactory_myPyPolicy := &pythonbridge.BridgeFactory{
    StreamManager: pythonbridge.GetStreamManager(),
    PolicyName:    "my-python-policy",
    PolicyVersion: "v1.0.0",
    Mode: policy.ProcessingMode{
        RequestHeaderMode:  policy.HeaderModeProcess,
        RequestBodyMode:    policy.BodyModeBuffer,
        ResponseHeaderMode: policy.HeaderModeSkip,
        ResponseBodyMode:   policy.BodyModeSkip,
    },
}
registry.GetRegistry().Register(policyDef_myPyPolicy, bridgeFactory_myPyPolicy.GetPolicy)
```

This means the Go template needs two sections: one for Go policies (with imports), one for Python policies (with BridgeFactory creation). The `PolicyImport` struct in `registry.go` must gain a `Runtime` field (`"go"` or `"python"`), and when `Runtime == "python"`, the template emits the bridge factory pattern instead. The ProcessingMode fields must be passed through the template data.

---

## 7. Python-Side: Executor Service

### 7.1 Architecture

The Python Executor is a long-running gRPC server. It:
1. Starts up, loads all Python policy modules
2. Listens on `/var/run/api-platform/python-executor.sock`
3. Handles the `ExecuteStream` RPC — receives `ExecutionRequest` messages, instantiates/caches policy objects, calls `on_request()` or `on_response()`, sends back `ExecutionResponse`

### 7.2 Directory Structure

```
gateway/gateway-runtime/python-executor/
├── executor/
│   ├── __init__.py
│   ├── server.py          # gRPC server main loop
│   ├── policy_loader.py   # Discovers and imports Python policy modules
│   ├── policy_cache.py    # Lazy content-addressed instance cache
│   ├── context.py         # Python context/action classes (mirrors Go SDK)
│   └── translator.py      # Proto ↔ Python object translation
├── proto/
│   ├── __init__.py
│   └── python_executor_pb2.py      # Generated
│   └── python_executor_pb2_grpc.py # Generated
├── main.py                # Entry point
├── requirements.txt       # grpcio, protobuf, + merged policy deps
└── sdk/
    ├── __init__.py
    └── policy.py           # Python Policy ABC (abstract base class)
```

### 7.3 `main.py`

```python
"""Python Executor entry point.

Starts the gRPC server on UDS, loads all registered Python policies,
and serves ExecuteStream RPCs from the Go Policy Engine.
"""
import asyncio
import os
import signal
import logging

from executor.server import PythonExecutorServer

SOCKET_PATH = os.environ.get(
    "PYTHON_EXECUTOR_SOCKET",
    "/var/run/api-platform/python-executor.sock"
)
WORKER_COUNT = int(os.environ.get("PYTHON_POLICY_WORKERS", "4"))
LOG_LEVEL = os.environ.get("LOG_LEVEL", "info").upper()

async def main():
    logging.basicConfig(level=getattr(logging, LOG_LEVEL, logging.INFO))
    server = PythonExecutorServer(SOCKET_PATH, WORKER_COUNT)

    # Graceful shutdown on SIGTERM
    loop = asyncio.get_event_loop()
    for sig in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(sig, lambda: asyncio.create_task(server.shutdown()))

    await server.start()
    await server.wait_for_termination()

if __name__ == "__main__":
    asyncio.run(main())
```

### 7.4 `server.py`

Key responsibilities:
- Creates an `asyncio` gRPC server with `grpc.aio`
- Implements `ExecuteStream` as an async generator
- Uses a `ThreadPoolExecutor` (configurable size via `PYTHON_POLICY_WORKERS` env var, default 4) for running policy code (which may be CPU-bound or blocking I/O)
- For each incoming `ExecutionRequest`, submits execution to the thread pool, awaits the result, sends back `ExecutionResponse`

```python
class PythonExecutorServicer(PythonExecutorServiceServicer):
    def __init__(self, policy_loader, policy_cache, worker_count):
        self.loader = policy_loader
        self.cache = policy_cache
        self.executor = ThreadPoolExecutor(max_workers=worker_count)

    async def ExecuteStream(self, request_iterator, context):
        async for request in request_iterator:
            try:
                response = await asyncio.get_event_loop().run_in_executor(
                    self.executor,
                    self._execute_policy,
                    request
                )
                yield response
            except Exception as e:
                yield self._error_response(request.request_id, e)

    def _execute_policy(self, request):
        # 1. Get or create policy instance from cache
        # 2. Translate proto context → Python context objects
        # 3. Call policy.on_request() or policy.on_response()
        # 4. Translate Python action → proto response
        # 5. Return ExecutionResponse
```

### 7.5 `policy_loader.py`

At startup, the Python Executor needs to know which policies exist. The Gateway Builder generates a `python_policy_registry.py` file that maps policy names to their module import paths:

```python
# Auto-generated by Gateway Builder. DO NOT EDIT.
PYTHON_POLICIES = {
    "my-python-policy:v1": "policies.my_python_policy.policy",
    "another-python-policy:v2": "policies.another_python_policy.policy",
}
```

The policy loader:
1. Imports the registry
2. For each entry, uses `importlib.import_module()` to load the module
3. Looks for a `get_policy` factory function in each module
4. Stores the factory function for later use by the cache

### 7.6 `policy_cache.py` — Lazy Content-Addressed Cache

Unlike Go where policy instances are created eagerly per-route during xDS updates, Python uses **lazy, content-addressed caching**:

- **Why lazy?** Python doesn't receive xDS updates. Instances are created on first execution request.
- **Why content-addressed?** Different routes may use the same policy with the same parameters. Instead of creating one instance per route (wasteful), we cache by `(policy_name, policy_version, hash(sorted_params))`. If two routes use `rate-limiter:v1` with identical params, they share one Python instance.

```python
class PolicyCache:
    def __init__(self, policy_loader):
        self._cache = {}       # (name, version, params_hash) → policy instance
        self._lock = threading.Lock()
        self._loader = policy_loader

    def get_or_create(self, name, version, metadata, params):
        params_hash = self._hash_params(params)
        key = (name, version, params_hash)

        with self._lock:
            if key in self._cache:
                return self._cache[key]

        # Create outside lock (factory may be slow)
        factory = self._loader.get_factory(name, version)
        instance = factory(metadata, params)

        with self._lock:
            # Double-check after creation
            if key not in self._cache:
                self._cache[key] = instance
            return self._cache[key]

    def _hash_params(self, params):
        # Stable hash of sorted, serialized params
        import json, hashlib
        serialized = json.dumps(params, sort_keys=True, default=str)
        return hashlib.sha256(serialized.encode()).hexdigest()
```

---

## 8. Python SDK Design

Create: `gateway/gateway-runtime/python-executor/sdk/policy.py`

This mirrors the Go SDK concepts but in Pythonic style:

```python
"""Python Policy SDK — mirrors the Go sdk/gateway/policy/v1alpha interface."""

from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from typing import Dict, List, Optional, Any
from enum import Enum


# ──────────────── Processing Mode ────────────────

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


# ──────────────── Context Objects ────────────────

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


# ──────────────── Action Types ────────────────

@dataclass
class UpstreamRequestModifications:
    """Continue request to upstream with modifications."""
    set_headers: Dict[str, str] = field(default_factory=dict)
    remove_headers: List[str] = field(default_factory=list)
    append_headers: Dict[str, List[str]] = field(default_factory=dict)
    body: Optional[bytes] = None        # None = no change
    path: Optional[str] = None          # None = no change
    method: Optional[str] = None        # None = no change
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
    body: Optional[bytes] = None        # None = no change
    status_code: Optional[int] = None   # None = no change
    analytics_metadata: Dict[str, Any] = field(default_factory=dict)

# Union types for return values
RequestAction = Optional[UpstreamRequestModifications | ImmediateResponse]
ResponseAction = Optional[UpstreamResponseModifications]


# ──────────────── Policy Metadata ────────────────

@dataclass
class PolicyMetadata:
    route_name: str = ""
    api_id: str = ""
    api_name: str = ""
    api_version: str = ""
    attached_to: str = ""   # "api" or "route"


# ──────────────── Policy ABC ────────────────

class Policy(ABC):
    """Abstract base class for all Python policies.

    All Python policies must subclass this and implement on_request() and on_response().
    The mode() method is optional — if not overridden, it returns the default
    (process request headers only).

    Policy instances are cached. The constructor (__init__) should perform all
    initialization, validation, and preprocessing. on_request/on_response should
    be stateless relative to the policy's configuration (though they can read self.params).
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
        """Declare processing requirements. Override if you need body access or response processing."""
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
```

### 8.1 Policy Module Convention

Each Python policy must be a directory with:
```
my-python-policy/
├── policy-definition.yaml    # Same format as Go, but with runtime: python
├── policy.py                 # Must contain a get_policy() factory function
├── requirements.txt          # Optional — pip dependencies
└── (other .py files)         # Supporting modules
```

The `policy.py` must export:
```python
from sdk.policy import Policy, PolicyMetadata

class MyPolicy(Policy):
    def __init__(self, metadata, params):
        super().__init__(metadata, params)
        # Initialize from params
        self.some_setting = params.get("someSetting", "default")

    def on_request(self, ctx, params):
        # ... policy logic ...
        return UpstreamRequestModifications(set_headers={"X-Custom": "value"})

    def on_response(self, ctx, params):
        return None  # Pass-through


def get_policy(metadata: PolicyMetadata, params: dict) -> Policy:
    """Factory function — called by the Python Executor to create instances."""
    return MyPolicy(metadata, params)
```

---

## 9. Build System Changes

### 9.1 `build.yaml` Format Extension

Add a new `pythonmodule:` key alongside the existing `gomodule:` and `filePath:`:

```yaml
version: v1
policies:
  # Go policy (existing)
  - name: jwt-auth
    gomodule: github.com/wso2/gateway-controllers/policies/jwt-auth@v0

  # Python policy via tarball (remote GitHub repository)
  - name: my-python-policy
    pythonmodule: github.com/wso2/python-policies/my-python-policy@v1.0.0

  # Python policy via local filePath (for development)
  - name: my-local-python-policy
    filePath: ./dev-policies/my-local-python-policy
```

For remote Python policies, `pythonmodule:` specifies a GitHub reference. The builder will download it as a **tarball** (not git clone, to avoid `.git` overhead):
- Parse `github.com/<owner>/<repo>/<path>@<tag>` 
- Download `https://github.com/<owner>/<repo>/archive/refs/tags/<tag>.tar.gz`
- Extract the specific policy directory from the tarball

For `filePath:` entries, the builder checks `policy-definition.yaml` for `runtime: python` to determine if it's a Python policy.

### 9.2 `BuildEntry` Changes

File: `gateway/gateway-builder/pkg/types/manifest.go`

```go
type BuildEntry struct {
    Name         string `yaml:"name"`
    FilePath     string `yaml:"filePath,omitempty"`
    Gomodule     string `yaml:"gomodule,omitempty"`
    Pythonmodule string `yaml:"pythonmodule,omitempty"`   // NEW
}
```

### 9.3 `DiscoveredPolicy` Changes

File: `gateway/gateway-builder/pkg/types/policy.go`

```go
type DiscoveredPolicy struct {
    Name             string
    Version          string
    Path             string
    YAMLPath         string
    GoModPath        string                    // Empty for Python policies
    SourceFiles      []string
    SystemParameters map[string]interface{}
    Definition       *policy.PolicyDefinition
    GoModulePath     string
    GoModuleVersion  string
    IsFilePathEntry  bool

    // NEW fields for Python support
    Runtime          string                    // "go" (default) or "python"
    PythonSourceDir  string                    // Path to Python source directory
    ProcessingMode   *policy.ProcessingMode    // Parsed from policy-definition.yaml (Python only)
}
```

### 9.4 `PolicyDefinition` Changes

File: `sdk/gateway/policy/v1alpha/definition.go`

Add optional fields for Python policies:

```go
type PolicyDefinition struct {
    Name             string                 `yaml:"name" json:"name"`
    Version          string                 `yaml:"version" json:"version"`
    Parameters       map[string]interface{} `yaml:"parameters" json:"parameters"`
    SystemParameters map[string]interface{} `yaml:"systemParameters" json:"systemParameters"`

    // NEW fields
    Runtime          string                 `yaml:"runtime,omitempty" json:"runtime,omitempty"`            // "go" (default) or "python"
    ProcessingModeConfig *ProcessingModeConfig `yaml:"processingMode,omitempty" json:"processingMode,omitempty"` // Required for python runtime
}

// ProcessingModeConfig is the YAML-parseable version of ProcessingMode
type ProcessingModeConfig struct {
    RequestHeaderMode  string `yaml:"requestHeaderMode,omitempty"`   // "SKIP" or "PROCESS"
    RequestBodyMode    string `yaml:"requestBodyMode,omitempty"`     // "SKIP" or "BUFFER"
    ResponseHeaderMode string `yaml:"responseHeaderMode,omitempty"`  // "SKIP" or "PROCESS"
    ResponseBodyMode   string `yaml:"responseBodyMode,omitempty"`    // "SKIP" or "BUFFER"
}
```

### 9.5 Discovery Changes

File: `gateway/gateway-builder/internal/discovery/manifest.go`

The `validateBuildFile` function must be updated:
- Currently requires `filePath` or `gomodule`. Now also accept `pythonmodule`.
- If both `gomodule` and `pythonmodule` are set → error
- Validation message: "either filePath, gomodule, or pythonmodule must be provided"

The `DiscoverPoliciesFromBuildFile` function must handle the new `pythonmodule` case:
- For `pythonmodule:` entries: download the tarball, extract to a temp directory, discover the policy from there
- Parse `policy-definition.yaml` 
- Check `runtime` field — if `"python"`, set `DiscoveredPolicy.Runtime = "python"`
- For `filePath:` entries: after parsing `policy-definition.yaml`, check if `runtime == "python"`

File: `gateway/gateway-builder/internal/discovery/policy.go`

The `ValidateDirectoryStructure` function currently requires `go.mod` and `.go` files. For Python policies, it should instead require `policy.py` (or at least `.py` files). The function must accept a `runtime` parameter or check the `policy-definition.yaml` first.

New function: `ValidatePythonDirectoryStructure(policyDir string) error` — checks for `policy-definition.yaml` + `policy.py`.

New function: `CollectPythonSourceFiles(policyDir string) ([]string, error)` — finds all `.py` files.

### 9.6 New Discovery Function: Fetch Python Module

File: `gateway/gateway-builder/internal/discovery/python_module.go` (new file)

```go
// FetchPythonModule downloads a Python policy from a GitHub tarball reference.
// Format: "github.com/<owner>/<repo>/<path>@<version>"
//
// Steps:
//   1. Parse the reference into owner, repo, path, version
//   2. Download https://github.com/<owner>/<repo>/archive/refs/tags/<version>.tar.gz
//   3. Extract to a temp directory
//   4. Return the path to the extracted policy directory
func FetchPythonModule(pythonmodule string) (string, error) { ... }
```

### 9.7 Code Generation Changes

File: `gateway/gateway-builder/internal/policyengine/generator.go`

The `GenerateCode` function currently generates only Go artifacts. It must be extended to also:

1. **Generate `python_policy_registry.py`**: A Python file mapping policy names to module paths (for the Python Executor's policy loader).

2. **Copy Python policy source files**: Copy each Python policy's source directory into a staging area (e.g., `/workspace/output/python-policies/<policy-name>/`).

3. **Merge `requirements.txt`**: Concatenate all Python policies' `requirements.txt` files into one combined file. Duplicates are fine — `pip` will resolve them.

4. **Copy the Python SDK and Executor source**: Copy `gateway/gateway-runtime/python-executor/` to the output.

Updated `GenerateCode`:
```go
func GenerateCode(srcDir string, policies []*types.DiscoveredPolicy) error {
    // Separate Go and Python policies
    goPolicies := filterByRuntime(policies, "go")
    pythonPolicies := filterByRuntime(policies, "python")

    // Existing Go generation (unchanged)
    // ... GeneratePluginRegistry, GenerateBuildInfo, UpdateGoMod ...

    // NEW: Python generation (only if there are Python policies)
    if len(pythonPolicies) > 0 {
        if err := GeneratePythonArtifacts(srcDir, pythonPolicies); err != nil {
            return err
        }
    }

    return nil
}

func GeneratePythonArtifacts(srcDir string, pythonPolicies []*types.DiscoveredPolicy) error {
    // 1. Copy Python executor source to output staging
    // 2. Copy each policy's Python source into output/python-policies/<name>/
    // 3. Generate python_policy_registry.py
    // 4. Merge requirements.txt files
    return nil
}
```

File: `gateway/gateway-builder/internal/policyengine/registry.go`

The `PolicyImport` struct and `GeneratePluginRegistry` must be extended:

```go
type PolicyImport struct {
    Name             string
    Version          string
    ImportPath       string
    ImportAlias      string
    SystemParameters map[string]interface{}
    Runtime          string                    // NEW: "go" or "python"
    ProcessingMode   *policy.ProcessingMode    // NEW: only for Python policies
}
```

`GeneratePluginRegistry` must pass `Runtime` and `ProcessingMode` through to the template, and only include Go import paths for Go policies.

### 9.8 Template Changes

File: `gateway/gateway-builder/templates/plugin_registry.go.tmpl`

The template must handle both Go and Python policies:

```go
// Code generated by Gateway Builder. DO NOT EDIT.
package main

import (
    "context"
    "log/slog"
    "os"

    "github.com/wso2/api-platform/gateway/gateway-runtime/policy-engine/internal/registry"
    policy "github.com/wso2/api-platform/sdk/gateway/policy/v1alpha"
{{- if .HasPythonPolicies }}
    "github.com/wso2/api-platform/gateway/gateway-runtime/policy-engine/internal/pythonbridge"
{{- end }}
{{- range .GoPolicies }}
    {{ .ImportAlias }} "{{ .ImportPath }}"
{{- end }}
)

func init() {
    slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
    ctx := context.Background()
    slog.InfoContext(ctx, "Registering policies from Builder compilation")

    // Register Go policies
    {{- range .GoPolicies }}
    policyDef_{{ .ImportAlias }} := &policy.PolicyDefinition{
        Name:             "{{ .Name }}",
        Version:          "{{ .Version }}",
        SystemParameters: {{ formatSystemParams .SystemParameters }},
    }
    if err := registry.GetRegistry().Register(policyDef_{{ .ImportAlias }}, {{ .ImportAlias }}.GetPolicy); err != nil {
        slog.ErrorContext(ctx, "Failed to register policy", "name", "{{ .Name }}", "error", err)
    }
    {{- end }}

    // Register Python policies via PythonBridge
    {{- range .PythonPolicies }}
    policyDef_{{ .ImportAlias }} := &policy.PolicyDefinition{
        Name:             "{{ .Name }}",
        Version:          "{{ .Version }}",
        SystemParameters: {{ formatSystemParams .SystemParameters }},
    }
    bridgeFactory_{{ .ImportAlias }} := &pythonbridge.BridgeFactory{
        StreamManager: pythonbridge.GetStreamManager(),
        PolicyName:    "{{ .Name }}",
        PolicyVersion: "{{ .Version }}",
        Mode: policy.ProcessingMode{
            RequestHeaderMode:  {{ .ProcessingMode.RequestHeaderMode }},
            RequestBodyMode:    {{ .ProcessingMode.RequestBodyMode }},
            ResponseHeaderMode: {{ .ProcessingMode.ResponseHeaderMode }},
            ResponseBodyMode:   {{ .ProcessingMode.ResponseBodyMode }},
        },
    }
    if err := registry.GetRegistry().Register(policyDef_{{ .ImportAlias }}, bridgeFactory_{{ .ImportAlias }}.GetPolicy); err != nil {
        slog.ErrorContext(ctx, "Failed to register Python policy", "name", "{{ .Name }}", "error", err)
    }
    {{- end }}
}
```

### 9.9 StreamManager Initialization

The `StreamManager` singleton must be lazily initialized. When the first PythonBridge is used (or at startup if Python policies exist), it connects to the Python Executor's UDS socket.

Add to `pythonbridge/client.go`:

```go
var (
    globalStreamManager *StreamManager
    streamManagerOnce   sync.Once
)

func GetStreamManager() *StreamManager {
    streamManagerOnce.Do(func() {
        socketPath := os.Getenv("PYTHON_EXECUTOR_SOCKET")
        if socketPath == "" {
            socketPath = "/var/run/api-platform/python-executor.sock"
        }
        globalStreamManager = NewStreamManager(socketPath)
    })
    return globalStreamManager
}
```

Connection is deferred until `Execute()` is first called (lazy connect), with retry logic.

---

## 10. Dockerfile Changes

File: `gateway/gateway-runtime/Dockerfile`

The Dockerfile needs a new stage for Python dependencies and must install Python in the runtime stage.

### 10.1 New Stage: Python Dependencies

Add between the policy-compiler stage and the runtime stage:

```dockerfile
# Stage 2.5: Python Dependencies (only when Python policies exist)
FROM python:3.11-slim-bookworm AS python-deps

WORKDIR /python-executor

# Copy the merged requirements.txt from the builder output
COPY --from=policy-compiler /workspace/output/python-executor/requirements.txt ./requirements.txt

# Install all Python dependencies into a known location
RUN pip install --no-cache-dir --target /python-libs -r requirements.txt

# Copy the Python executor source and policies
COPY --from=policy-compiler /workspace/output/python-executor/ /python-executor/
```

### 10.2 Runtime Stage Changes

In the existing Stage 3 (runtime), add:

```dockerfile
# Install Python 3.11 minimal runtime (no pip needed — deps are pre-installed)
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    apt-get update && apt-get install -y --no-install-recommends \
    python3 \
    python3-distutils \
    libpython3-stdlib

# Copy pre-installed Python dependencies
COPY --from=python-deps /python-libs /app/python-libs

# Copy Python executor + policies + SDK
COPY --from=python-deps /python-executor /app/python-executor

# Set PYTHONPATH so Python can find the libs and executor modules
ENV PYTHONPATH="/app/python-libs:/app/python-executor"
```

### 10.3 Conditional Python Support

The builder should set a flag or marker file indicating whether Python policies are present. The Dockerfile can use multi-stage conditional building, or simply always include Python support (simpler, ~30MB overhead from python3 package). 

**Decision**: Always include Python runtime in the Docker image when any `pythonmodule:` entry exists in `build.yaml`. The Gateway Builder's Dockerfile generation phase should conditionally include the Python stages.

The `DockerfileGenerator` in `gateway/gateway-builder/internal/docker/` must be updated to:
1. Check if any discovered policies have `Runtime == "python"`
2. If yes, include the Python-deps stage and Python-related COPY/RUN commands in the generated Dockerfiles

---

## 11. Entrypoint Script Changes

File: `gateway/gateway-runtime/docker-entrypoint.sh`

The script currently manages **2 processes**. With Python policies, it manages **3 processes**.

### 11.1 New Argument Prefix

Add `--py.*` prefix support for Python Executor arguments:

```bash
PY_ARGS=()
# In the while loop:
--py.*)
    PY_ARGS+=("--${1#--py.}")
    shift
    if [[ $# -gt 0 && "$1" != --* ]]; then
        PY_ARGS+=("$1")
        shift
    fi
    ;;
```

### 11.2 Python Executor Startup

Before starting the Go Policy Engine, start the Python Executor:

```bash
PYTHON_EXECUTOR_SOCKET="/var/run/api-platform/python-executor.sock"

# Check if Python executor is needed (marker file set by builder)
if [ -f /app/python-executor/main.py ]; then
    log "Starting Python Executor..."
    export PYTHON_EXECUTOR_SOCKET
    python3 /app/python-executor/main.py "${PY_ARGS[@]}" \
        > >(while IFS= read -r line; do echo "[pye] $line"; done) \
        2> >(while IFS= read -r line; do echo "[pye] $line" >&2; done) &
    PY_PID=$!
    log "Python Executor started (PID $PY_PID)"

    # Wait for Python socket
    SOCKET_WAIT_TIMEOUT=15
    SOCKET_WAIT_COUNT=0
    while [ ! -S "${PYTHON_EXECUTOR_SOCKET}" ]; do
        if [ $SOCKET_WAIT_COUNT -ge $SOCKET_WAIT_TIMEOUT ]; then
            log "ERROR: Python Executor socket not created within ${SOCKET_WAIT_TIMEOUT}s"
            exit 1
        fi
        if ! kill -0 "$PY_PID" 2>/dev/null; then
            log "ERROR: Python Executor exited before creating socket"
            exit 1
        fi
        sleep 1
        SOCKET_WAIT_COUNT=$((SOCKET_WAIT_COUNT + 1))
    done
    log "Python Executor socket ready: ${PYTHON_EXECUTOR_SOCKET}"
else
    PY_PID=""
    log "No Python policies detected, skipping Python Executor"
fi
```

### 11.3 Updated Process Monitoring

The `wait -n` call must include the Python PID:

```bash
if [ -n "$PY_PID" ]; then
    wait -n "$PY_PID" "$PE_PID" "$ENVOY_PID"
else
    wait -n "$PE_PID" "$ENVOY_PID"
fi
EXIT_CODE=$?
```

### 11.4 Updated Shutdown Handler

Kill all 3 processes on shutdown:

```bash
shutdown() {
    log "Received shutdown signal, terminating processes..."
    for pid_var in PY_PID PE_PID ENVOY_PID; do
        pid="${!pid_var}"
        if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
            log "Stopping $pid_var (PID $pid)..."
            kill -TERM "$pid" 2>/dev/null || true
        fi
    done
    wait
    rm -f "${POLICY_ENGINE_SOCKET}" "${PYTHON_EXECUTOR_SOCKET}"
    log "Shutdown complete"
    exit 0
}
```

---

## 12. Caching Strategy

### 12.1 Go-Side (Existing, Unchanged)

Go policy instances are created **eagerly per-route** during xDS updates. When the Gateway Controller pushes new route configs, the Policy Engine calls `PolicyRegistry.CreateInstance()` for each route-policy combination, building complete `PolicyChain` objects. These are stored in `Kernel.Routes` and atomically swapped.

This applies to **both Go and Python bridge policies**. The Go `PythonBridge` instance itself is "created" (a lightweight struct) per route. But the Bridge doesn't call Python at creation time — it only calls Python when `OnRequest()`/`OnResponse()` is invoked during actual request processing.

### 12.2 Python-Side (New, Lazy Content-Addressed)

On the Python side, actual policy instances (which may do expensive initialization like loading ML models, compiling regex patterns, etc.) are cached **lazily by content**:

- Key: `(policy_name, policy_version, sha256(sorted_json(params)))`
- Created on first `ExecuteStream` request that needs this combination
- Reused for subsequent requests with the same key

This means: if the same policy with the same params is used on 100 different routes, only one Python instance exists. Conversely, if the same policy is used with different params on different routes, separate instances are created.

### 12.3 Cache Invalidation

No cache invalidation. When routes change (xDS update), Go recreates PythonBridge instances with potentially new params. If the params changed, the new hash won't match, and a new Python instance is created on next execution. Old instances will be garbage collected when no longer referenced (Go bridges holding old params are discarded during `ApplyWholeRoutes()`). The Python cache entries for old params can be cleaned up periodically via a TTL or LRU eviction strategy. For V1, rely on the fact that params rarely change.

---

## 13. Error Handling

### 13.1 Principles

1. **Python errors must not crash the Go Policy Engine.** All gRPC calls use timeouts. Python panics/exceptions are caught by the executor and returned as `ExecutionError` responses.

2. **Policy execution errors return 500 to client.** If a Python policy raises an exception, the Bridge translates the `ExecutionError` proto to a `policy.ImmediateResponse{StatusCode: 500}`. The Go error handling middleware adds an error ID for correlation.

3. **Python Executor crash**: If the Python process dies, the Go `StreamManager` detects the broken stream. Subsequent calls return errors, which translate to 500 responses. The entrypoint script detects the process exit and shuts down all processes.

4. **Timeout**: The `StreamManager.Execute()` call uses a configurable timeout (default: 30s, via `PYTHON_POLICY_TIMEOUT` env var). On timeout, an error is returned and logged.

### 13.2 Error Response Proto

```protobuf
message ExecutionError {
    string message = 1;
    string policy_name = 2;
    string policy_version = 3;
    string error_type = 4;    // "init_error", "execution_error", "timeout"
}
```

### 13.3 Init Errors

If a Python policy's `get_policy()` factory fails (e.g., missing dependency, bad config), the `PolicyCache` catches the exception and caches a sentinel error. All subsequent requests to that policy return the same init error without retrying.

---

## 14. Metrics and Observability

### 14.1 Go-Side Metrics (Prometheus)

The existing metrics in `gateway/gateway-runtime/policy-engine/internal/metrics/` already track per-policy execution time, success/failure counts, etc. Since PythonBridge is just another `Policy` implementation, these metrics automatically apply. The policy name/version labels will correctly identify Python policies.

Additional metrics to add:
- `python_bridge_grpc_duration_seconds` — histogram of Go→Python gRPC call latency
- `python_bridge_stream_errors_total` — counter of stream reconnection events
- `python_bridge_pending_requests` — gauge of in-flight requests to Python

### 14.2 Python-Side Logging

Python logs to stdout/stderr with `[pye]` prefix (via entrypoint piping). Use structured JSON logging matching the Go format:
```python
import logging, json
class JsonFormatter(logging.Formatter):
    def format(self, record):
        return json.dumps({"time": ..., "level": ..., "msg": record.getMessage(), ...})
```

---

## 15. Implementation Phases

### Phase 1: Foundation (Proto + PythonBridge + Python Executor skeleton)

1. Define the proto file (`python_executor.proto`)
2. Generate Go and Python protobuf code
3. Implement Go `StreamManager` (client connecting to UDS, bidi streaming, request correlation)
4. Implement Go `PythonBridge` struct (Policy interface, delegates to StreamManager)
5. Implement Go `BridgeFactory` (creates PythonBridge instances)
6. Implement Python Executor skeleton (`main.py`, `server.py`, gRPC server with `ExecuteStream`)
7. Implement Python SDK (`sdk/policy.py` with ABC, context, action dataclasses)
8. Implement Python `policy_loader.py` and `policy_cache.py`
9. Implement Python `translator.py` (proto ↔ Python objects)
10. **Test**: Write a simple Python policy, manually start both processes, verify end-to-end execution

### Phase 2: Build System Integration

1. Add `Pythonmodule` field to `BuildEntry` in `manifest.go`
2. Add `Runtime`, `PythonSourceDir`, `ProcessingMode` to `DiscoveredPolicy` in `policy.go`
3. Add `Runtime`, `ProcessingModeConfig` to `PolicyDefinition` in `definition.go`
4. Implement `FetchPythonModule()` in `discovery/python_module.go`
5. Update `ValidateDirectoryStructure()` / add `ValidatePythonDirectoryStructure()`
6. Update `DiscoverPoliciesFromBuildFile()` to handle `pythonmodule:` entries
7. Update `GenerateCode()` to separate Go/Python policies and call `GeneratePythonArtifacts()`
8. Implement `GeneratePythonArtifacts()` — copy source, generate registry, merge requirements.txt
9. Update `plugin_registry.go.tmpl` template for dual Go/Python registration
10. Update `PolicyImport` struct with `Runtime` and `ProcessingMode` fields
11. **Test**: Run the builder with a mixed `build.yaml` (Go + Python), verify generated files

### Phase 3: Docker + Entrypoint

1. Update `Dockerfile` with Python deps stage and Python runtime installation
2. Update `docker-entrypoint.sh` for 3-process management
3. Update Dockerfile generation templates if applicable
4. **Test**: Build the Docker image, start it, verify all 3 processes run and communicate

### Phase 4: Integration Testing

1. Create a sample Python policy (e.g., `add-python-header` that adds a custom header)
2. Add it to the integration test's `build.yaml`
3. Write integration test feature files testing:
   - Python policy applies header modifications
   - Python policy with body buffering
   - Mixed chain (Go + Python policies in same chain)
   - Python policy returning ImmediateResponse
   - Python policy error handling (bad config, runtime exception)
4. Run `cd gateway/it && make test`

### Phase 5: Hardening

1. Implement reconnection logic in StreamManager (exponential backoff)
2. Add timeout configuration (`PYTHON_POLICY_TIMEOUT` env var)
3. Add Go-side Prometheus metrics for Python bridge
4. Implement structured JSON logging in Python
5. Performance benchmarking and optimization

---

## 16. File-by-File Change Inventory

### New Files to Create

| File | Language | Purpose |
|------|----------|---------|
| `gateway/gateway-runtime/proto/python_executor.proto` | Protobuf | gRPC contract between Go and Python |
| `gateway/gateway-runtime/policy-engine/internal/pythonbridge/bridge.go` | Go | PythonBridge Policy implementation |
| `gateway/gateway-runtime/policy-engine/internal/pythonbridge/client.go` | Go | StreamManager gRPC client |
| `gateway/gateway-runtime/policy-engine/internal/pythonbridge/factory.go` | Go | BridgeFactory for creating PythonBridge instances |
| `gateway/gateway-runtime/policy-engine/internal/pythonbridge/translator.go` | Go | Proto ↔ Go SDK type translation |
| `gateway/gateway-runtime/policy-engine/internal/pythonbridge/proto/` | Go | Generated protobuf Go code (directory) |
| `gateway/gateway-runtime/python-executor/main.py` | Python | Python Executor entry point |
| `gateway/gateway-runtime/python-executor/executor/__init__.py` | Python | Package init |
| `gateway/gateway-runtime/python-executor/executor/server.py` | Python | gRPC server implementation |
| `gateway/gateway-runtime/python-executor/executor/policy_loader.py` | Python | Policy discovery and import |
| `gateway/gateway-runtime/python-executor/executor/policy_cache.py` | Python | Lazy content-addressed cache |
| `gateway/gateway-runtime/python-executor/executor/translator.py` | Python | Proto ↔ Python object translation |
| `gateway/gateway-runtime/python-executor/executor/context.py` | Python | (Optional) Context helper functions |
| `gateway/gateway-runtime/python-executor/sdk/__init__.py` | Python | SDK package init |
| `gateway/gateway-runtime/python-executor/sdk/policy.py` | Python | Policy ABC, context/action dataclasses |
| `gateway/gateway-runtime/python-executor/requirements.txt` | Text | Base requirements (grpcio, protobuf) |
| `gateway/gateway-builder/internal/discovery/python_module.go` | Go | Python module tarball fetcher |
| `gateway/gateway-builder/templates/python_policy_registry.py.tmpl` | Template | Template for generating Python policy registry |
| `gateway/sample-policies/add-python-header/policy-definition.yaml` | YAML | Sample Python policy definition |
| `gateway/sample-policies/add-python-header/policy.py` | Python | Sample Python policy implementation |

### Existing Files to Modify

| File | Changes |
|------|---------|
| `gateway/gateway-builder/pkg/types/manifest.go` | Add `Pythonmodule` field to `BuildEntry` |
| `gateway/gateway-builder/pkg/types/policy.go` | Add `Runtime`, `PythonSourceDir`, `ProcessingMode` to `DiscoveredPolicy` |
| `sdk/gateway/policy/v1alpha/definition.go` | Add `Runtime`, `ProcessingModeConfig` to `PolicyDefinition` |
| `gateway/gateway-builder/internal/discovery/manifest.go` | Handle `pythonmodule:` entries in validation and discovery |
| `gateway/gateway-builder/internal/discovery/policy.go` | Add Python directory validation, support runtime detection |
| `gateway/gateway-builder/internal/policyengine/generator.go` | Add `GeneratePythonArtifacts()`, update `GenerateCode()` to separate Go/Python |
| `gateway/gateway-builder/internal/policyengine/registry.go` | Add `Runtime`, `ProcessingMode` to `PolicyImport`, separate Go/Python in generation |
| `gateway/gateway-builder/templates/plugin_registry.go.tmpl` | Add Python bridge registration section |
| `gateway/gateway-builder/templates/templates.go` | Add embed for new Python registry template |
| `gateway/gateway-runtime/Dockerfile` | Add Python deps stage, install Python in runtime |
| `gateway/gateway-runtime/docker-entrypoint.sh` | Add 3rd process management, `--py.*` args, Python socket waiting |
| `gateway/build.yaml` | (When adding a Python policy entry for testing) |

---

## 17. Testing Strategy

### 17.1 Unit Tests

- **Go PythonBridge**: Mock the StreamManager, verify `OnRequest`/`OnResponse` correctly build proto requests and translate responses
- **Go StreamManager**: Test request correlation, timeout handling, reconnection
- **Go Builder discovery**: Test `pythonmodule:` parsing, tarball download (with mock HTTP server), directory validation for Python policies
- **Go Builder code gen**: Test that generated `plugin_registry.go` includes bridge factory registrations
- **Python Executor**: Test policy loading, caching, proto translation
- **Python SDK**: Test action dataclass serialization

### 17.2 Integration Tests

Location: `gateway/it/features/`

Create feature files testing Python policy scenarios:
- Basic Python policy that modifies request headers
- Python policy with body access (BUFFER mode)
- Mixed Go + Python policy chain (verify ordering, metadata sharing)
- Python policy returning ImmediateResponse (short-circuit)
- Python policy error handling (exception → 500 response)
- Shared metadata: Go policy writes metadata, Python policy reads it (and vice versa)

### 17.3 Running Tests

```bash
# Unit tests
cd gateway/gateway-runtime/policy-engine && go test ./internal/pythonbridge/...
cd gateway/gateway-builder && go test ./...

# Integration tests (follow repo's copilot-instructions.md)
cd gateway && make build-local
cd gateway/it && make test
```

---

## 18. Decisions Log

All design decisions and their justifications:

| # | Decision | Choice | Justification |
|---|----------|--------|---------------|
| 1 | Go↔Python communication | gRPC over UDS | Low latency (~50-100μs), strong contract via proto, language agnostic, streaming support |
| 2 | Who is gRPC server? | Python is server, Go is client | Go initiates calls (it drives the policy chain). Python listens and responds |
| 3 | Stream type | Single bidirectional stream with request ID correlation | Avoids per-call overhead. Request IDs allow concurrent multiplexing. Pool as future optimization |
| 4 | Python concurrency | AsyncIO + ThreadPoolExecutor | AsyncIO for gRPC I/O, ThreadPoolExecutor for potentially blocking policy code |
| 5 | Thread pool size | Configurable via `PYTHON_POLICY_WORKERS` env var (default: 4) | Different deployments have different needs |
| 6 | Python caching | Lazy content-addressed: `(name, version, hash(params))` | Python doesn't get xDS updates; content-addressed deduplicates identical instances across routes |
| 7 | Go caching | Unchanged (eager per-route during xDS) | PythonBridge is just another Policy, created per-route. Lightweight struct, no Python call at creation |
| 8 | ProcessingMode for Python | Static, declared in `policy-definition.yaml` | Go needs mode at chain-build time to set `RequiresRequestBody` flags, without calling Python |
| 9 | System parameter resolution | Go-side, before sending to Python | Python never sees `${config}` strings. Consistent with Go behavior. Reuses existing `ConfigResolver` |
| 10 | Python version | 3.11 | Good balance of features, performance, and stability. Dataclass improvements, type hints |
| 11 | Dependency management | Shared virtual environment, pip resolves at build time | Per-policy venvs don't provide real isolation (Python's `sys.modules` is global). Shared is simpler |
| 12 | Requirements merging | Gateway Builder concatenates all `requirements.txt`, pip resolves | Simple, correct (pip handles version conflicts). Done at build time, not runtime |
| 13 | Python policy fetching | GitHub tarball download | Simpler than git clone, no `.git` overhead, no git dependency needed in builder |
| 14 | Params serialization | `google.protobuf.Struct` | Native proto well-known type, maps to dict in Python and map[string]interface{} in Go |
| 15 | Hot reload | Not supported (V1) | Simplicity. Restart container for policy changes. Same as Go behavior |
| 16 | Graceful shutdown | SIGTERM propagation to all 3 processes | Entrypoint traps SIGTERM, sends to each PID. Python server has graceful shutdown via `asyncio` |
| 17 | Metrics | Go-side Prometheus only (initially) | PythonBridge is measured by existing Go metrics. Add Python-specific gRPC latency metrics |
| 18 | SDK distribution | Source code in repository, no PyPI | Simplest approach. Copied into Docker image at build time. No package registry needed |
| 19 | Container model | 3 processes in 1 container (same as current 2-process model, +1) | Maintains existing architecture pattern. Avoids cross-container networking complexity |
| 20 | Response execution order | Reverse (same as Go) | Python policies participate in the same chain. Go executor handles the ordering. Python just executes individual OnRequest/OnResponse calls |

---

## Appendix A: Sample Python Policy

### `policy-definition.yaml`

```yaml
name: add-python-header
version: v1.0.0
description: Adds a custom header to requests. Demonstrates a basic Python policy.
runtime: python

processingMode:
  requestHeaderMode: PROCESS
  requestBodyMode: SKIP
  responseHeaderMode: SKIP
  responseBodyMode: SKIP

parameters:
  type: object
  properties:
    headerName:
      type: string
      description: Name of the header to add
      default: "X-Python-Policy"
    headerValue:
      type: string
      description: Value of the header
      default: "hello-from-python"

systemParameters:
  type: object
  properties: {}
```

### `policy.py`

```python
from sdk.policy import (
    Policy, PolicyMetadata, RequestContext, ResponseContext,
    UpstreamRequestModifications, RequestAction, ResponseAction
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
        return None  # Pass-through


def get_policy(metadata: PolicyMetadata, params: dict) -> Policy:
    return AddPythonHeaderPolicy(metadata, params)
```

### `requirements.txt`

```
# No extra dependencies for this simple policy
```

---

## Appendix B: Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `PYTHON_EXECUTOR_SOCKET` | `/var/run/api-platform/python-executor.sock` | UDS path for Python gRPC server |
| `PYTHON_POLICY_WORKERS` | `4` | ThreadPoolExecutor max workers |
| `PYTHON_POLICY_TIMEOUT` | `30s` | Per-call timeout for Go→Python gRPC |
| `LOG_LEVEL` | `info` | Shared across all processes |

---

## Appendix C: Proto Compilation Commands

```bash
# From gateway/gateway-runtime/proto/
# Generate Go code:
protoc --go_out=../policy-engine/internal/pythonbridge/proto \
       --go-grpc_out=../policy-engine/internal/pythonbridge/proto \
       --go_opt=paths=source_relative \
       --go-grpc_opt=paths=source_relative \
       python_executor.proto

# Generate Python code:
python -m grpc_tools.protoc \
       -I. \
       --python_out=../python-executor/proto \
       --grpc_python_out=../python-executor/proto \
       python_executor.proto
```

---

## Appendix D: Key Patterns to Preserve

When implementing, maintain consistency with the existing codebase:

1. **Copyright headers**: Every file starts with the WSO2 Apache 2.0 copyright block (see any existing file)
2. **Structured logging**: Use `slog` in Go with `"phase"` field (e.g., `slog.Info("message", "phase", "discovery")`)
3. **Error wrapping**: Use `fmt.Errorf("context: %w", err)` for Go errors
4. **Package-level errors**: Builder uses custom error types from `gateway/gateway-builder/pkg/errors/` (e.g., `errors.NewDiscoveryError()`, `errors.NewGenerationError()`)
5. **Template embedding**: Templates live in `gateway/gateway-builder/templates/` and are embedded via `//go:embed` in `templates.go`
6. **Policy naming**: Policy names use kebab-case (`my-policy`), Go import aliases use snake_case with underscores (`my_policy_v1_0_0`)
7. **Composite keys**: Registry uses `name:majorVersion` format (e.g., `"jwt-auth:v1"`)
8. **Thread safety comments**: Document thread safety guarantees in comments (see `PolicyRegistry` and `Kernel` for examples)
