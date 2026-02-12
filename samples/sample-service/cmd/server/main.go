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

package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/wso2/api-platform/samples/sample-service/internal/handler"
)

var version = "dev"

func main() {
	addr := flag.String("addr", ":8080", "server listen address")
	pretty := flag.Bool("pretty", false, "pretty print JSON responses")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	h := &handler.Handler{Pretty: *pretty}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	slog.Info("starting sample-service", "version", version, "addr", *addr, "pretty", *pretty)

	server := &http.Server{
		Addr:         *addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	if err := server.ListenAndServe(); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}
