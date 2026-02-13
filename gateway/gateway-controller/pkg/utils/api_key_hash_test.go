/*
 * Copyright (c) 2025, WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

package utils

import (
	"github.com/wso2/api-platform/gateway/gateway-controller/pkg/config"
	"github.com/wso2/api-platform/gateway/gateway-controller/pkg/constants"
	"strings"
	"testing"
)


func TestSHA256APIKeyHashing(t *testing.T) {
	// Create service with SHA256 hashing configuration
	service := &APIKeyService{
		apiKeyConfig: &config.APIKeyConfig{
			APIKeysPerUserPerAPI: 10,
			Algorithm:            constants.HashingAlgorithmSHA256,
		},
	}

	plainKey := "apip_test123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	// Test SHA256 hashing
	hashedKey, err := service.hashAPIKey(plainKey)
	if err != nil {
		t.Fatalf("Failed to hash API key with SHA256: %v", err)
	}

	// Verify the hashed key is different from plain key
	if hashedKey == plainKey {
		t.Error("SHA256 hashed key should be different from plain key")
	}

	// Verify the hash starts with SHA256 prefix
	if !strings.HasPrefix(hashedKey, "$sha256$") {
		t.Error("SHA256 hashed key should start with $sha256$ prefix")
	}

	// Test validation with correct key
	valid := service.compareAPIKeys(plainKey, hashedKey)
	if !valid {
		t.Error("SHA256 validation should succeed with correct plain key")
	}

	// Test validation with incorrect key
	wrongKey := "apip_wrong123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	valid = service.compareAPIKeys(wrongKey, hashedKey)
	if valid {
		t.Error("SHA256 validation should fail with incorrect plain key")
	}
}

func TestSHA256APIKeyHashDeterminism(t *testing.T) {
	// Create service with SHA256 hashing configuration
	service := &APIKeyService{
		apiKeyConfig: &config.APIKeyConfig{
			APIKeysPerUserPerAPI: 10,
			Algorithm:            constants.HashingAlgorithmSHA256,
		},
	}
	plainKey := "apip_test123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	// Generate two SHA256 hashes of the same key
	hash1, err := service.hashAPIKey(plainKey)
	if err != nil {
		t.Fatalf("Failed to hash API key with SHA256 (1): %v", err)
	}

	hash2, err := service.hashAPIKey(plainKey)
	if err != nil {
		t.Fatalf("Failed to hash API key with SHA256 (2): %v", err)
	}

	// Hashes should be different due to random salt
	if hash1 == hash2 {
		t.Error("Two SHA256 hashes of the same key should be different (SHA256 uses random salt)")
	}

	// But both should validate against the same plain key
	if !service.compareAPIKeys(plainKey, hash1) {
		t.Error("First SHA256 hash should validate correctly")
	}

	if !service.compareAPIKeys(plainKey, hash2) {
		t.Error("Second SHA256 hash should validate correctly")
	}
}

func TestAPIKeyHashingDefaultBehavior(t *testing.T) {
	// Create service with no algorithm specified (should default to SHA256)
	service := &APIKeyService{
		apiKeyConfig: &config.APIKeyConfig{
			APIKeysPerUserPerAPI: 10,
			Algorithm:            "", // Empty algorithm defaults to SHA256
		},
	}

	plainKey := "apip_test123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	// Test hashing with empty algorithm - should default to SHA256
	result, err := service.hashAPIKey(plainKey)
	if err != nil {
		t.Fatalf("Failed to hash API key with default algorithm: %v", err)
	}

	// With empty algorithm, should default to SHA256 hashing
	if result == plainKey {
		t.Error("Empty algorithm should default to SHA256 hashing, not return plain key")
	}

	// Should start with SHA256 prefix since it defaults to SHA256
	if !strings.HasPrefix(result, "$sha256$") {
		t.Error("Default algorithm should produce SHA256 hash with $sha256$ prefix")
	}

	// Test validation with default SHA256 algorithm
	valid := service.compareAPIKeys(plainKey, result)
	if !valid {
		t.Error("Validation should succeed with default SHA256 algorithm")
	}

	// Test validation with wrong key
	wrongKey := "apip_wrong123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	valid = service.compareAPIKeys(wrongKey, result)
	if valid {
		t.Error("Validation should fail with wrong key when using default algorithm")
	}

	// Test empty keys
	_, err = service.hashAPIKey("")
	if err == nil {
		t.Error("Hashing empty key should return error")
	}

	valid = service.compareAPIKeys("", result)
	if valid {
		t.Error("Validation should fail with empty plain key")
	}

	valid = service.compareAPIKeys(plainKey, "")
	if valid {
		t.Error("Validation should fail with empty stored key")
	}
}

func TestAPIKeyHashingDefaultBehaviorDeterminism(t *testing.T) {
	// Create service with no algorithm specified (should default to SHA256)
	service := &APIKeyService{
		apiKeyConfig: &config.APIKeyConfig{
			APIKeysPerUserPerAPI: 10,
			Algorithm:            "", // Empty algorithm defaults to SHA256
		},
	}
	plainKey := "apip_test123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	// Generate hashes multiple times with default algorithm (SHA256)
	result1, err := service.hashAPIKey(plainKey)
	if err != nil {
		t.Fatalf("Failed to hash API key with default algorithm (1): %v", err)
	}

	result2, err := service.hashAPIKey(plainKey)
	if err != nil {
		t.Fatalf("Failed to hash API key with default algorithm (2): %v", err)
	}

	// Results should be different due to random salt in SHA256
	if result1 == result2 {
		t.Error("SHA256 hashes should be different due to random salt")
	}

	// Both should be SHA256 hashes, not plain keys
	if result1 == plainKey || result2 == plainKey {
		t.Error("Default algorithm should produce SHA256 hashes, not plain keys")
	}

	// Both should start with SHA256 prefix
	if !strings.HasPrefix(result1, "$sha256$") || !strings.HasPrefix(result2, "$sha256$") {
		t.Error("Default algorithm should produce SHA256 hashes with proper prefix")
	}

	// Both should validate correctly against the same plain key
	if !service.compareAPIKeys(plainKey, result1) {
		t.Error("First SHA256 hash should validate correctly")
	}

	if !service.compareAPIKeys(plainKey, result2) {
		t.Error("Second SHA256 hash should validate correctly")
	}
}

func TestHashingConfigurationSwitching(t *testing.T) {
	service := &APIKeyService{}
	plainKey := "apip_test123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	// Test with empty algorithm (defaults to SHA256)
	service.SetHashingConfig(&config.APIKeyConfig{
		APIKeysPerUserPerAPI: 10,
		Algorithm:            "", // Empty algorithm defaults to SHA256
	})
	defaultResult, err := service.hashAPIKey(plainKey)
	if err != nil {
		t.Fatalf("Failed to hash API key with default algorithm: %v", err)
	}
	// Should be SHA256 hash, not plain key
	if defaultResult == plainKey {
		t.Error("Default algorithm should hash the key, not return plain key")
	}
	if !strings.HasPrefix(defaultResult, "$sha256$") {
		t.Error("Default algorithm should produce SHA256 hash")
	}

	// Test switching to SHA256
	service.SetHashingConfig(&config.APIKeyConfig{
		APIKeysPerUserPerAPI: 10,
		Algorithm:            constants.HashingAlgorithmSHA256,
	})
	sha256Result, err := service.hashAPIKey(plainKey)
	if err != nil {
		t.Fatalf("Failed to hash with SHA256: %v", err)
	}
	if !strings.HasPrefix(sha256Result, "$sha256$") {
		t.Error("SHA256 hash should start with $sha256$ prefix")
	}

	// Validate that the hash works with the same plain key
	service.SetHashingConfig(&config.APIKeyConfig{
		APIKeysPerUserPerAPI: 10,
		Algorithm:            constants.HashingAlgorithmSHA256,
	})
	if !service.compareAPIKeys(plainKey, sha256Result) {
		t.Error("SHA256 hash should validate correctly")
	}
}

func TestAPIKeyHashingMixedScenario(t *testing.T) {
	// Test scenario where we have mixed hash formats and algorithm comparison
	service := &APIKeyService{
		apiKeyConfig: &config.APIKeyConfig{
			APIKeysPerUserPerAPI: 10,
			Algorithm:            "", // Empty algorithm defaults to SHA256
		},
	}

	plainKey := "apip_test123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	// Generate new key with current default algorithm (SHA256)
	newHashedKey, err := service.hashAPIKey(plainKey)
	if err != nil {
		t.Fatalf("Failed to hash key with default algorithm: %v", err)
	}

	// New key should be SHA256 format
	if !strings.HasPrefix(newHashedKey, "$sha256$") {
		t.Error("Default algorithm should produce SHA256 hash")
	}

	// Plain key should validate against the newly generated SHA256 hash
	valid := service.compareAPIKeys(plainKey, newHashedKey)
	if !valid {
		t.Error("Plain key should validate against its SHA256 hash")
	}

	// Test with SHA256 format hash (should still be validated by compareAPIKeys)
	sha256Hash := "$sha256$73616c74$68617368" // Example format
	valid = service.compareAPIKeys(plainKey, sha256Hash)
	if valid {
		t.Error("Plain key should not validate against invalid SHA256 hash")
	}
}

func TestMixedAPIKeyFormatsValidation(t *testing.T) {
	// Test scenario where we have keys in different formats:
	// - Plain text keys (legacy)
	// - SHA256 hashed keys

	plainKey1 := "apip_plain123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	plainKey2 := "apip_test456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef01"

	// Generate hashes using different algorithms

	// 1. Plain text key (simulate legacy storage)
	service := &APIKeyService{
		apiKeyConfig: &config.APIKeyConfig{
			APIKeysPerUserPerAPI: 10,
			Algorithm:            "", // Empty algorithm defaults to SHA256
		},
	}
	plainHashed, err := service.hashAPIKey(plainKey1)
	if err != nil {
		t.Fatalf("Failed to hash with default algorithm: %v", err)
	}

	// 2. SHA256 hashed key
	service.SetHashingConfig(&config.APIKeyConfig{
		APIKeysPerUserPerAPI: 10,
		Algorithm:            constants.HashingAlgorithmSHA256,
	})
	sha256Hashed, err := service.hashAPIKey(plainKey2)
	if err != nil {
		t.Fatalf("Failed to hash key with SHA256: %v", err)
	}

	// Reset service to simulate runtime validation
	service.SetHashingConfig(&config.APIKeyConfig{
		APIKeysPerUserPerAPI: 10,
		Algorithm:            constants.HashingAlgorithmSHA256, // Current default
	})

	// Test validation of each key format

	// 1. Validate first hashed key
	valid := service.compareAPIKeys(plainKey1, plainHashed)
	if !valid {
		t.Error("Plain key should validate against its hash")
	}

	// 2. Validate SHA256 hashed key
	valid = service.compareAPIKeys(plainKey2, sha256Hashed)
	if !valid {
		t.Error("Plain key should validate against SHA256 hash")
	}

	// Verify SHA256 format
	if !strings.HasPrefix(sha256Hashed, "$sha256$") {
		t.Error("SHA256 hash should start with $sha256$ prefix")
	}

	// Test cross-validation (should fail)

	// Plain key 1 should not validate against other hashes
	valid = service.compareAPIKeys(plainKey1, sha256Hashed)
	if valid {
		t.Error("Wrong plain key should not validate against SHA256 hash")
	}

	// Plain key 2 should not validate against other hashes
	valid = service.compareAPIKeys(plainKey2, plainHashed)
	if valid {
		t.Error("Wrong plain key should not validate against different hash")
	}

	// Test with completely wrong keys
	wrongKey := "apip_wrong56789abcdef0123456789abcdef0123456789abcdef0123456789abcdef01234"

	valid = service.compareAPIKeys(wrongKey, plainHashed)
	if valid {
		t.Error("Wrong key should not validate against hash")
	}

	valid = service.compareAPIKeys(wrongKey, sha256Hashed)
	if valid {
		t.Error("Wrong key should not validate against SHA256 hash")
	}

	// Test empty key scenarios
	valid = service.compareAPIKeys("", sha256Hashed)
	if valid {
		t.Error("Empty key should not validate")
	}

	valid = service.compareAPIKeys(plainKey1, "")
	if valid {
		t.Error("Key should not validate against empty hash")
	}
}

func TestMixedAPIKeyFormatsValidationWithDefaultAlgorithm(t *testing.T) {
	// Test mixed formats when using default algorithm (SHA256)
	service := &APIKeyService{
		apiKeyConfig: &config.APIKeyConfig{
			APIKeysPerUserPerAPI: 10,
			Algorithm:            "", // Empty algorithm defaults to SHA256
		},
	}

	plainKey := "apip_test123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	// Simulate pre-existing hashed keys
	sha256Hash := "$sha256$73616c74$abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"

	// compareAPIKeys should handle various hash formats regardless of current algorithm
	// Plain key should not validate against invalid hashes
	valid := service.compareAPIKeys(plainKey, sha256Hash)
	if valid {
		t.Error("Plain key should not validate against invalid SHA256 hash")
	}

	// Test that we generate SHA256 hash with default algorithm
	result, err := service.hashAPIKey(plainKey)
	if err != nil {
		t.Fatalf("Should be able to hash key with default algorithm: %v", err)
	}

	// Should be SHA256 hash, not plain key
	if result == plainKey {
		t.Error("Default algorithm should hash the key, not return plain key")
	}

	if !strings.HasPrefix(result, "$sha256$") {
		t.Error("Default algorithm should produce SHA256 hash")
	}

	// The generated hash should validate against the plain key
	valid = service.compareAPIKeys(plainKey, result)
	if !valid {
		t.Error("Generated SHA256 hash should validate against the original plain key")
	}
}

func TestHashingConfigurationGetSet(t *testing.T) {
	// Initialize service with a default configuration
	defaultConfig := &config.APIKeyConfig{
		APIKeysPerUserPerAPI: 10,
		Algorithm:            "", // Empty algorithm defaults to SHA256
	}
	service := &APIKeyService{
		apiKeyConfig: defaultConfig,
	}

	// Test default configuration
	retrievedDefaultConfig := service.GetHashingConfig()
	if retrievedDefaultConfig.Algorithm != "" {
		t.Error("Default hashing config should have empty algorithm (defaults to SHA256)")
	}
	if retrievedDefaultConfig.APIKeysPerUserPerAPI != 10 {
		t.Error("Default API keys per user per API should be 10")
	}

	// Test setting configuration
	newConfig := config.APIKeyConfig{
		APIKeysPerUserPerAPI: 5,
		Algorithm:            constants.HashingAlgorithmSHA256,
	}
	service.SetHashingConfig(&newConfig)

	retrievedConfig := service.GetHashingConfig()
	if retrievedConfig.APIKeysPerUserPerAPI != newConfig.APIKeysPerUserPerAPI {
		t.Error("API keys per user per API should match")
	}
	if retrievedConfig.Algorithm != newConfig.Algorithm {
		t.Error("Hashing config algorithm should match")
	}
}
