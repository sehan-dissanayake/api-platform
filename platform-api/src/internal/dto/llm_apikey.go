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

package dto

// CreateLLMProviderAPIKeyRequest represents the request to create an API key for an LLM provider.
type CreateLLMProviderAPIKeyRequest struct {
	// Name is the unique identifier for this API key within the LLM provider
	Name string `json:"name,omitempty"`

	// DisplayName is the user-friendly name of the API key
	DisplayName string `json:"displayName,omitempty"`

	// ExpiresAt is the optional expiration time in ISO 8601 format
	ExpiresAt *string `json:"expiresAt,omitempty"`
}

// CreateLLMProviderAPIKeyResponse represents the response after creating an API key for an LLM provider.
type CreateLLMProviderAPIKeyResponse struct {
	// Status indicates the result of the operation
	Status string `json:"status"`

	// Message provides additional details about the operation result
	Message string `json:"message"`

	// KeyId is the unique identifier of the generated key
	KeyId string `json:"keyId,omitempty"`

	// ApiKey is the generated API key value (returned only once)
	ApiKey string `json:"apiKey,omitempty"`
}
