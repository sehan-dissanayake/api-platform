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
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/wso2/api-platform/samples/sample-service/internal/types"
)

// Request returns the incoming request details as JSON and captures it for inspection.
func (h *Handler) Request(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	defer r.Body.Close()

	info := types.RequestInfo{
		Method:  r.Method,
		Path:    r.URL.Path,
		Query:   r.URL.RawQuery,
		Headers: r.Header,
	}
	if len(body) > 0 {
		info.Body = string(body)
	}

	h.mu.Lock()
	h.lastRequest = &info
	h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if codeStr := r.URL.Query().Get("statusCode"); codeStr != "" {
		if code, err := strconv.Atoi(codeStr); err == nil {
			if code >= 100 && code <= 999 {
				w.WriteHeader(code)
			}
		}
	}
	h.writeJSON(w, info)
}

// GetCapturedRequest returns the last request received by the Request handler.
func (h *Handler) GetCapturedRequest(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	last := h.lastRequest
	h.mu.RUnlock()

	if last == nil {
		slog.Info("captured-request: no request captured yet")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	slog.Info("captured-request: returning last captured request")
	logEntry := struct {
		Method string          `json:"method"`
		Path   string          `json:"path"`
		Query  string          `json:"query,omitempty"`
		Body   json.RawMessage `json:"body,omitempty"`
	}{
		Method: last.Method,
		Path:   last.Path,
		Query:  last.Query,
	}
	if last.Body != "" {
		var raw json.RawMessage
		if json.Unmarshal([]byte(last.Body), &raw) == nil {
			logEntry.Body = raw
		} else {
			logEntry.Body, _ = json.Marshal(last.Body)
		}
	}
	b, _ := json.MarshalIndent(logEntry, "", "  ")
	fmt.Println(string(b))
	w.Header().Set("Content-Type", "application/json")
	h.writeJSON(w, last)
}
