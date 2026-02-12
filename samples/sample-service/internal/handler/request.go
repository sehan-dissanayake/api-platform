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
	"io"
	"net/http"

	"github.com/wso2/api-platform/samples/sample-service/internal/types"
)

// Request returns the incoming request details as JSON.
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

	w.Header().Set("Content-Type", "application/json")
	h.writeJSON(w, info)
}
