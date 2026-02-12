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

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wso2/api-platform/samples/sample-service/internal/types"
)

func TestRequestReturnsMethod(t *testing.T) {
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
	h := &Handler{}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/test", nil)
			rec := httptest.NewRecorder()
			h.Request(rec, req)

			var info types.RequestInfo
			if err := json.NewDecoder(rec.Body).Decode(&info); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if info.Method != method {
				t.Errorf("expected method %q, got %q", method, info.Method)
			}
		})
	}
}

func TestRequestReturnsPathAndQuery(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/pets?species=dog&limit=10", nil)
	rec := httptest.NewRecorder()
	h.Request(rec, req)

	var info types.RequestInfo
	if err := json.NewDecoder(rec.Body).Decode(&info); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if info.Path != "/pets" {
		t.Errorf("expected path %q, got %q", "/pets", info.Path)
	}
	if info.Query != "species=dog&limit=10" {
		t.Errorf("expected query %q, got %q", "species=dog&limit=10", info.Query)
	}
}

func TestRequestReturnsHeaders(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Custom-Header", "custom-value")
	req.Header.Set("Authorization", "Bearer token123")
	rec := httptest.NewRecorder()
	h.Request(rec, req)

	var info types.RequestInfo
	if err := json.NewDecoder(rec.Body).Decode(&info); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got := info.Headers["X-Custom-Header"]; len(got) == 0 || got[0] != "custom-value" {
		t.Errorf("expected header X-Custom-Header=%q, got %v", "custom-value", got)
	}
	if got := info.Headers["Authorization"]; len(got) == 0 || got[0] != "Bearer token123" {
		t.Errorf("expected header Authorization=%q, got %v", "Bearer token123", got)
	}
}

func TestRequestReturnsBody(t *testing.T) {
	h := &Handler{}
	body := `{"name":"petstore","version":"v1"}`
	req := httptest.NewRequest(http.MethodPost, "/apis", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Request(rec, req)

	var info types.RequestInfo
	if err := json.NewDecoder(rec.Body).Decode(&info); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if info.Body != body {
		t.Errorf("expected body %q, got %q", body, info.Body)
	}
}

func TestRequestOmitsEmptyBody(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.Request(rec, req)

	// Decode into raw map to check body field is omitted
	var raw map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if _, exists := raw["body"]; exists {
		t.Error("expected body field to be omitted for empty body")
	}
}

func TestRequestContentType(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.Request(rec, req)

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type %q, got %q", "application/json", ct)
	}
}
