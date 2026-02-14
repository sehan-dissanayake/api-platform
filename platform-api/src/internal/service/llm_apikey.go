/*
 *  Copyright (c) 2026, WSO2 LLC. (http://www.wso2.org) All Rights Reserved.
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

package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"platform-api/src/api"
	"platform-api/src/internal/constants"
	"platform-api/src/internal/model"
	"platform-api/src/internal/repository"
	"platform-api/src/internal/utils"
)

// LLMProviderAPIKeyService handles API key management for LLM providers
type LLMProviderAPIKeyService struct {
	llmProviderRepo      repository.LLMProviderRepository
	gatewayRepo          repository.GatewayRepository
	gatewayEventsService *GatewayEventsService
}

// NewLLMProviderAPIKeyService creates a new LLM provider API key service instance
func NewLLMProviderAPIKeyService(
	llmProviderRepo repository.LLMProviderRepository,
	gatewayRepo repository.GatewayRepository,
	gatewayEventsService *GatewayEventsService,
) *LLMProviderAPIKeyService {
	return &LLMProviderAPIKeyService{
		llmProviderRepo:      llmProviderRepo,
		gatewayRepo:          gatewayRepo,
		gatewayEventsService: gatewayEventsService,
	}
}

// CreateLLMProviderAPIKey generates an API key for an LLM provider and broadcasts it to all gateways.
func (s *LLMProviderAPIKeyService) CreateLLMProviderAPIKey(
	ctx context.Context,
	providerID, orgID, userID string,
	req *api.CreateLLMProviderAPIKeyRequest,
) (*api.CreateLLMProviderAPIKeyResponse, error) {

	provider, err := s.llmProviderRepo.GetByID(providerID, orgID)
	if err != nil {
		log.Printf("[ERROR] Failed to get LLM provider for API key creation: providerID=%s error=%v", providerID, err)
		return nil, fmt.Errorf("failed to get LLM provider: %w", err)
	}
	if provider == nil {
		log.Printf("[WARN] LLM provider not found: providerID=%s orgID=%s", providerID, orgID)
		return nil, constants.ErrAPINotFound
	}

	apiKey, err := utils.GenerateAPIKey()
	if err != nil {
		log.Printf("[ERROR] Failed to generate API key for LLM provider: providerID=%s error=%v", providerID, err)
		return nil, fmt.Errorf("failed to generate API key: %w", err)
	}

	var name string
	if req.Name != nil && *req.Name != "" {
		name = *req.Name
	} else {
		displayName := ""
		if req.DisplayName != nil {
			displayName = *req.DisplayName
		}
		if displayName == "" {
			log.Printf("[ERROR] Failed to generate API key name: providerID=%s error=%v", providerID, constants.ErrHandleSourceEmpty)
			return nil, fmt.Errorf("failed to generate API key name: both name and displayName are empty: %w", constants.ErrHandleSourceEmpty)
		}

		name, err = utils.GenerateHandle(displayName, nil)
		if err != nil {
			log.Printf("[ERROR] Failed to generate API key name: providerID=%s error=%v", providerID, err)
			return nil, fmt.Errorf("failed to generate API key name: %w", err)
		}
	}

	displayName := name
	if req.DisplayName != nil && *req.DisplayName != "" {
		displayName = *req.DisplayName
	}

	var expiresAt *string
	if req.ExpiresAt != nil {
		expiresAtStr := req.ExpiresAt.Format(time.RFC3339)
		expiresAt = &expiresAtStr
	}

	if displayName == "" {
		displayName = name
	}

	gateways, err := s.gatewayRepo.GetByOrganizationID(orgID)
	if err != nil {
		log.Printf("[ERROR] Failed to get gateways for API key broadcast: providerID=%s error=%v", providerID, err)
		return nil, fmt.Errorf("failed to get gateways: %w", err)
	}

	if len(gateways) == 0 {
		log.Printf("[WARN] No gateways found for organization: orgID=%s", orgID)
		return nil, constants.ErrGatewayUnavailable
	}

	operations := "[\"*\"]"

	event := &model.APIKeyCreatedEvent{
		ApiId:       providerID,
		Name:        name,
		DisplayName: displayName,
		ApiKey:      apiKey,
		Operations:  operations,
		ExpiresAt:   expiresAt,
	}

	successCount := 0
	failureCount := 0
	var lastError error

	for _, gateway := range gateways {
		gatewayID := gateway.ID

		log.Printf("[INFO] Broadcasting LLM provider API key created event: providerID=%s gatewayID=%s keyName=%s",
			providerID, gatewayID, name)

		err := s.gatewayEventsService.BroadcastAPIKeyCreatedEvent(gatewayID, userID, event)
		if err != nil {
			failureCount++
			lastError = err
			log.Printf("[ERROR] Failed to broadcast LLM provider API key created event: providerID=%s gatewayID=%s keyName=%s error=%v",
				providerID, gatewayID, name, err)
		} else {
			successCount++
			log.Printf("[INFO] Successfully broadcast LLM provider API key created event: providerID=%s gatewayID=%s keyName=%s",
				providerID, gatewayID, name)
		}
	}

	log.Printf("[INFO] LLM provider API key creation broadcast summary: providerID=%s keyName=%s total=%d success=%d failed=%d",
		providerID, name, len(gateways), successCount, failureCount)

	if successCount == 0 {
		log.Printf("[ERROR] Failed to deliver LLM provider API key to any gateway: providerID=%s keyName=%s", providerID, name)
		return nil, fmt.Errorf("failed to deliver API key event to any gateway: %w", lastError)
	}

	return &api.CreateLLMProviderAPIKeyResponse{
		Status:  "success",
		Message: "API key created and broadcasted to gateways successfully",
		KeyId:   name,
		ApiKey:  apiKey,
	}, nil
}
