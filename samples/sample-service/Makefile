# Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
#
# WSO2 LLC. licenses this file to you under the Apache License,
# Version 2.0 (the "License"); you may not use this file except
# in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied. See the License for the
# specific language governing permissions and limitations
# under the License.

SHELL := /bin/bash

VERSION := $(shell cat VERSION)
DOCKER_REGISTRY ?= ghcr.io/wso2/api-platform
IMAGE_NAME := $(DOCKER_REGISTRY)/sample-service

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help message
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-25s %s\n", $$1, $$2}'

.PHONY: build
build: ## Build Docker image
	docker build -t $(IMAGE_NAME):$(VERSION) -t $(IMAGE_NAME):latest --build-arg VERSION=$(VERSION) .

.PHONY: run
run: build ## Build and run Docker container (use ARGS to pass flags, e.g. make run ARGS="-pretty -addr :9080")
	docker run --rm -p 8080:8080 $(IMAGE_NAME):$(VERSION) $(ARGS)

.PHONY: release
release: ## Build and push multi-arch Docker image (amd64, arm64)
	docker buildx build --platform linux/amd64,linux/arm64 \
		-t $(IMAGE_NAME):$(VERSION) \
		-t $(IMAGE_NAME):latest \
		--build-arg VERSION=$(VERSION) \
		--push .

.PHONY: test
test: ## Run all tests (unit + integration)
	go test -v -tags=integration ./... -cover

.PHONY: test-unit
test-unit: ## Run unit tests only
	go test -v ./... -cover

.PHONY: test-integration
test-integration: ## Run integration tests only
	go test -v -tags=integration ./test/... -cover

.PHONY: version
version: ## Show current version
	@echo $(VERSION)

.PHONY: version-set
version-set: ## Set version (usage: make version-set VERSION=x.y.z)
	@echo "$(VERSION)" > VERSION
	@echo "Version set to $(VERSION)"
