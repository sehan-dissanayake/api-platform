# WSO2 API Platform - Agent Guide

This document provides essential information for AI coding agents working on the WSO2 API Platform project.

## Project Overview

The WSO2 API Platform is an AI-ready, GitOps-driven API platform for full lifecycle management across cloud, hybrid, and on-premises deployments. It covers the complete API lifecycle including design, deployment, governance, and monetization.

### Key Principles

- **Developer experience is king**: Optimized workflows and UX for all users
- **Size matters, keep it as small as you can**: Minimal footprint for all components
- **Same control plane/UI experience across cloud and on-premises**: Consistent interface regardless of deployment model
- **Platform components are independent**: No hard dependencies between components
- **GitOps ready**: Configuration as code for both API configs and gateway configs
- **AI-Ready by design**: Servers are MCP enabled for AI agent integration
- **Docker as the shipping vehicle**: All components distributed via Docker containers
- **API Gateway based on Envoy Proxy**: Everything else is a policy

## Technology Stack

### Primary Languages
- **Go** (1.25+): Primary backend language for all services
- **TypeScript/React**: Frontend portals
- **Node.js**: Developer Portal backend

### Key Technologies
- **API Gateway**: Envoy Proxy 1.35.3 with custom Go-based Policy Engine
- **Control Plane**: Go with Gin framework, oapi-codegen for OpenAPI
- **Databases**: SQLite (development), PostgreSQL (production)
- **Message Queue**: Redis
- **Vector Database**: Milvus (for semantic search)
- **Container Orchestration**: Docker Compose (dev), Kubernetes with Helm (prod)
- **Observability**: OpenTelemetry, Jaeger, Prometheus, Grafana, OpenSearch

### Go Module Structure

The project uses Go workspaces (`go.work`) with the following modules:

```
./cli/it                     - CLI integration tests
./cli/src                    - CLI source code
./common                     - Shared libraries (auth, apikey, version)
./gateway/gateway-builder    - Policy compilation tooling
./gateway/gateway-controller - xDS control plane
./gateway/gateway-runtime/policy-engine - Envoy ext_proc policy engine
./gateway/it                 - Gateway integration tests
./gateway/sample-policies/*  - Sample policy implementations
./gateway/system-policies/analytics - Analytics system policy
./platform-api/src           - Platform API server
./samples/sample-service     - Sample backend service
./sdk                        - SDK for vector DB, caching
```

## Project Structure

```
api-platform-wso2/
├── cli/                     # Command-line interface
│   ├── src/                 # CLI source code (Cobra framework)
│   └── it/                  # Integration tests
├── common/                  # Shared Go libraries
├── gateway/                 # API Gateway components
│   ├── gateway-builder/     # Policy compilation tooling
│   ├── gateway-controller/  # xDS control plane (REST: 9090, xDS: 18000)
│   ├── gateway-runtime/     # Envoy + Policy Engine
│   ├── it/                  # Integration tests
│   ├── sample-policies/     # Example policies
│   └── system-policies/     # Built-in policies
├── platform-api/            # Platform API server (port 9243)
│   └── src/
│       ├── api/             # Generated API types
│       ├── cmd/             # Main entry point
│       ├── internal/        # Internal packages
│       │   ├── handler/     # HTTP handlers
│       │   ├── service/     # Business logic
│       │   ├── repository/  # Data access layer
│       │   └── websocket/   # WebSocket connections
│       └── resources/       # OpenAPI specs, templates
├── portals/                 # Web UI applications
│   ├── developer-portal/    # API consumer portal (Node.js/React)
│   └── management-portal/   # Admin portal (React/TypeScript/Vite)
├── kubernetes/              # Kubernetes deployment
│   ├── gateway-operator/    # K8s operator (Kubebuilder)
│   └── helm/                # Helm charts
├── distribution/            # Deployment artifacts
│   └── all-in-one/          # Docker Compose for full stack
├── sdk/                     # SDK for vector DB, Redis
├── samples/                 # Sample applications
├── scripts/                 # Build and release scripts
└── tests/                   # Test utilities and mock servers
```

## Build Commands

### Root Makefile Targets

```bash
# Version Management
make version                           # Show all component versions
make validate-versions                 # Validate version consistency

# Gateway
make build-gateway                     # Build all gateway Docker images
make build-and-push-gateway-multiarch  # Multi-arch build (amd64, arm64)
make test-gateway                      # Run gateway tests

# Platform API
make build-and-push-platform-api-multiarch VERSION=X  # Build platform-api
make test-platform-api                 # Run platform-api tests

# CLI
make build-cli                         # Build CLI binaries for all platforms
make test-cli                          # Run CLI tests

# Update image references
make update-images COMPONENT=X VERSION=Y  # Update docker-compose and Helm
```

### Gateway Makefile Targets (gateway/)

```bash
# Local builds (faster, single architecture)
make build-local                       # Build all gateway components locally
make build-local-controller            # Build controller only
make build-local-gateway-runtime       # Build runtime only
make build-local-gateway-builder       # Build builder only

# Multi-arch builds (for releases)
make build                             # Build all components with buildx
make build-and-push-multiarch          # Build and push multi-arch images

# Testing
make test                              # Run unit tests
make test-integration-all              # Build coverage images + run integration tests
make build-coverage                    # Build coverage-instrumented images

# Version management
make version-set VERSION=X.Y.Z         # Set version
make version-bump-patch                # Bump patch version
make version-bump-minor                # Bump minor version
make version-bump-major                # Bump major version
make version-bump-next-dev             # Bump to next dev version
```

### Platform API Makefile Targets (platform-api/)

```bash
make test                              # Run tests
make build                             # Build Docker image
make generate                          # Generate API server from OpenAPI spec
make build-and-push-multiarch          # Multi-arch build and push
```

### CLI Makefile Targets (cli/src/)

```bash
make build                             # Build for current platform (with tests)
make build-skip-tests                  # Build without running tests
make build-coverage                    # Build with coverage instrumentation
make build-all                         # Build for all platforms (macOS, Linux, Windows)
make test                              # Run tests with coverage
```

## Quick Start

```bash
# Start the full platform
cd distribution/all-in-one
docker compose up

# Create a default organization
curl -k --location 'https://localhost:9243/api/v1/organizations' \
  --header 'Content-Type: application/json' \
  --header 'Authorization: Bearer <shared-token>' \
  --data '{
    "id": "15b2ac94-6217-4f51-90d4-b2b3814b20b4",
    "handle": "acme",
    "name": "ACME Corporation",
    "region": "US"
}'

# Access the portals
# Management Portal: http://localhost:5173
# Developer Portal: http://localhost:3001
# Platform API: https://localhost:9243
```

## Code Style Guidelines

### Go Code Style

1. **Copyright Header**: Every Go file must include the Apache 2.0 license header:

```go
/*
 *  Copyright (c) 2025, WSO2 LLC. (http://www.wso2.org) All Rights Reserved.
 *
 *  Licensed under the Apache License, Version 2.0 (the "License");
 *  you may not use this file except in compliance with the License.
 *  You may obtain a copy of the License at
 *
 *  http://www.apache.org/licenses/LICENSE-2.0
 *
 *  Unless required by applicable law or agreed to in writing, software
 *  distributed under the License is distributed on an "AS IS" BASIS,
 *  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *  See the License for the specific language governing permissions and
 *  limitations under the License.
 *
 */
```

2. **Package Naming**: Use lowercase, no underscores. Prefer short, clear names.

3. **Import Organization**: Group imports in this order:
   - Standard library
   - Third-party packages
   - Internal project packages

4. **Error Handling**: Always check errors explicitly:
```go
if err != nil {
    return fmt.Errorf("contextual message: %w", err)
}
```

5. **Logging**: Use structured logging with `log/slog`:
```go
slogger.Info("message", "key", value)
slogger.Error("failed to do something", "error", err)
```

6. **Configuration**: Use `APIP_GW_` prefix for gateway environment variables.

### Testing

1. **Unit Tests**: Use `*_test.go` files alongside source files
2. **Test Naming**: `TestFunctionName_Scenario`
3. **Integration Tests**: Located in `it/` directories
4. **Coverage**: Tests must pass before builds complete

Example test:
```go
func TestService_Method(t *testing.T) {
    // Arrange
    svc := NewService()
    
    // Act
    result, err := svc.Method()
    
    // Assert
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if result != expected {
        t.Errorf("expected %v, got %v", expected, result)
    }
}
```

## Testing Instructions

### Unit Tests

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -v ./... -cover -coverprofile=coverage.txt

# Run tests for specific component
cd gateway/gateway-controller && go test ./...
cd platform-api/src && go test ./...
```

### Integration Tests

```bash
# Gateway integration tests (builds coverage images + runs tests)
cd gateway && make test-integration-all

# Run integration tests only (images must be pre-built)
cd gateway && make test-integration

# CLI integration tests
cd cli/it && go test ./...
```

### Test Mock Servers

The `tests/mock-servers/` directory contains mock implementations for:
- Analytics collector (Moesif-compatible)
- AWS Bedrock Guardrail
- Azure Content Safety
- Embedding provider (OpenAI-compatible)
- JWKS endpoint

## Configuration

### Gateway Controller

Environment variables use `APIP_GW_CONTROLLER_` prefix:

| Variable | Description | Default |
|----------|-------------|---------|
| `APIP_GW_CONTROLLER_STORAGE_TYPE` | Storage backend | `sqlite` |
| `APIP_GW_CONTROLLER_STORAGE_SQLITE_PATH` | SQLite database path | `./data/gateway.db` |
| `APIP_GW_CONTROLLER_LOGGING_LEVEL` | Log level | `info` |
| `APIP_GW_CONTROLLER_METRICS_PORT` | Metrics port | `9091` |

### Policy Engine

Environment variables use `APIP_GW_POLICY_ENGINE_` prefix.

### Gateway Runtime

| Variable | Description | Default |
|----------|-------------|---------|
| `GATEWAY_CONTROLLER_HOST` | Controller hostname | `gateway-controller` |
| `LOG_LEVEL` | Log level | `info` |

## Deployment

### Docker Compose (Development)

```bash
# Gateway only
cd gateway && docker compose up

# Full platform
cd distribution/all-in-one && docker compose up

# With observability profiles
docker compose --profile tracing --profile metrics --profile logging up
```

### Kubernetes

```bash
# Using Helm (see kubernetes/gateway-operator/HELM_QUICK_START.md)
cd kubernetes/gateway-operator
helm install gateway-operator ./helm/gateway-operator
```

## Version Management

Each component has its own `VERSION` file:
- Root: `VERSION`
- Gateway: `gateway/VERSION`
- Platform API: `platform-api/VERSION`
- CLI: `cli/VERSION`

Version format: `X.Y.Z-SNAPSHOT` for development, `X.Y.Z` for releases.

## Security Considerations

1. **API Keys**: API keys are stored hashed using SHA-256. Keys prefixed with `apip_` are validated.
2. **TLS**: All services use TLS by default. Self-signed certificates in development.
3. **Authentication**: JWT-based authentication with JWKS endpoint support.
4. **Authorization**: RBAC policies enforced at gateway level.
5. **Secrets**: Never commit secrets to version control. Use environment variables or mounted files.

## Common Development Tasks

### Adding a New API Endpoint

1. Update OpenAPI spec in `platform-api/src/resources/openapi.yaml`
2. Run `make generate` in `platform-api/` directory
3. Implement handler in `platform-api/src/internal/handler/`
4. Add service logic in `platform-api/src/internal/service/`
5. Add tests following existing patterns

### Adding a New Gateway Policy

1. Create new directory under `gateway/sample-policies/` or `gateway/system-policies/`
2. Implement policy as Go module with `go.mod`
3. Add policy configuration to `build.yaml`
4. Update gateway-builder to compile the policy
5. Add integration tests in `gateway/it/`

### Adding a New Portal Feature

For Management Portal (React/TypeScript):
1. Components in `portals/management-portal/src/`
2. Run `npm run dev` for development
3. Build with `npm run build`

For Developer Portal (Node.js/Express):
1. Routes and views in `portals/developer-portal/src/`
2. Static assets in `portals/developer-portal/src/pages/`
3. Run `npm start` for development

## Troubleshooting

### Common Issues

1. **Build fails with CGO errors**: Ensure you have gcc and libsqlite3-dev installed
2. **Docker build fails**: Check Docker Buildx is installed and running
3. **Integration tests fail**: Ensure all services are stopped before running tests
4. **Version conflicts**: Run `make validate-versions` to check consistency

### Debug Mode

Gateway controller supports debug logging:
```bash
APIP_GW_CONTROLLER_LOGGING_LEVEL=debug
```

## Additional Resources

- [Gateway README](gateway/README.md)
- [Gateway Controller README](gateway/gateway-controller/README.md)
- [Platform API README](platform-api/README.md)
- [CLI README](cli/src/README.md)
- [Gateway Operator Helm Guide](kubernetes/gateway-operator/HELM_QUICK_START.md)
