//go:build integration

// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied. See the License for the
// specific language governing permissions and limitations
// under the License.

package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wso2/api-platform/samples/sample-service/internal/handler"
	"github.com/wso2/api-platform/samples/sample-service/internal/types"
)

func newTestServer(pretty bool) *httptest.Server {
	h := &handler.Handler{Pretty: pretty}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return httptest.NewServer(mux)
}

func TestServerReturnsHeaders(t *testing.T) {
	srv := newTestServer(false)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/test", nil)
	req.Header.Set("X-Request-Id", "abc-123")
	req.Header.Set("Authorization", "Bearer secret-token")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var info types.RequestInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if got := info.Headers["X-Request-Id"]; len(got) == 0 || got[0] != "abc-123" {
		t.Errorf("expected X-Request-Id=%q, got %v", "abc-123", got)
	}
	if got := info.Headers["Authorization"]; len(got) == 0 || got[0] != "Bearer secret-token" {
		t.Errorf("expected Authorization=%q, got %v", "Bearer secret-token", got)
	}
}

func TestServerReturnsBody(t *testing.T) {
	srv := newTestServer(false)
	defer srv.Close()

	body := `{"name":"PetStore","version":"v1"}`
	resp, err := http.Post(srv.URL+"/apis", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var info types.RequestInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if info.Method != http.MethodPost {
		t.Errorf("expected method %q, got %q", http.MethodPost, info.Method)
	}
	if info.Path != "/apis" {
		t.Errorf("expected path %q, got %q", "/apis", info.Path)
	}
	if info.Body != body {
		t.Errorf("expected body %q, got %q", body, info.Body)
	}
}

func TestServerReturnsQueryParams(t *testing.T) {
	srv := newTestServer(false)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/pets?species=dog&limit=10")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var info types.RequestInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if info.Path != "/pets" {
		t.Errorf("expected path %q, got %q", "/pets", info.Path)
	}
	if info.Query != "species=dog&limit=10" {
		t.Errorf("expected query %q, got %q", "species=dog&limit=10", info.Query)
	}
}

func TestServerHealthEndpoint(t *testing.T) {
	srv := newTestServer(false)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result["status"] != "healthy" {
		t.Errorf("expected status %q, got %q", "healthy", result["status"])
	}
}

func TestServerPrettyPrint(t *testing.T) {
	srv := newTestServer(true)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "\n  ") {
		t.Error("expected pretty printed JSON with indentation")
	}
}

func TestServerAllMethods(t *testing.T) {
	srv := newTestServer(false)
	defer srv.Close()

	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req, _ := http.NewRequest(method, srv.URL+"/resource", nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			var info types.RequestInfo
			if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if info.Method != method {
				t.Errorf("expected method %q, got %q", method, info.Method)
			}
		})
	}
}
