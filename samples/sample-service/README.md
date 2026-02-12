# Sample Service

A simple HTTP service that returns request details (method, path, query, headers, body) as JSON. Used as a backend for testing the API Gateway.

## Endpoints

| Endpoint | Method | Description |
|---|---|---|
| `/health` | GET | Health check, returns `{"status": "healthy"}` |
| `/` | Any | Returns request info (method, path, query, headers, body) |

### Example Response

```
curl http://localhost:8080/pets?id=1
```

```json
{
  "method": "GET",
  "path": "/pets",
  "query": "id=1",
  "headers": {
    "Accept": ["*/*"],
    "User-Agent": ["curl/8.7.1"]
  }
}
```

## Flags

| Flag | Default | Description |
|---|---|---|
| `-addr` | `:8080` | Server listen address |
| `-pretty` | `false` | Pretty print JSON responses |

## Build

```bash
make build
```

## Run

```bash
make run

# With flags
make run ARGS="-pretty -addr :9080"
```

## Test

```bash
make test
```

## Release

Build and push multi-arch image (amd64, arm64) to registry:

```bash
make release
```
