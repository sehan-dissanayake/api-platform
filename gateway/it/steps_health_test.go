package it

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wso2/api-platform/gateway/it/steps"
)

func newHealthStepsForTest(controllerURL, policyEngineURL string) *HealthSteps {
	state := NewTestState()
	state.Config.GatewayControllerURL = controllerURL
	state.Config.GatewayControllerAdminURL = controllerURL
	state.Config.PolicyEngineURL = policyEngineURL
	httpSteps := steps.NewHTTPSteps(state.HTTPClient, map[string]string{})
	return &HealthSteps{
		state:     state,
		httpSteps: httpSteps,
	}
}

func TestHealthSteps_WaitForPolicySnapshotSync(t *testing.T) {
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass != "admin" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"policy_chain_version": "42",
		})
	}))
	defer controller.Close()

	policyEngine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"policy_chain_version": "42",
		})
	}))
	defer policyEngine.Close()

	h := newHealthStepsForTest(controller.URL, policyEngine.URL)
	require.NoError(t, h.waitForPolicySnapshotSync())
}

func TestHealthSteps_WaitForEndpointToBeReady_AlsoWaitsForSync(t *testing.T) {
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"policy_chain_version": "7",
		})
	}))
	defer controller.Close()

	policyEngine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"policy_chain_version": "7",
		})
	}))
	defer policyEngine.Close()

	readyEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer readyEndpoint.Close()

	h := newHealthStepsForTest(controller.URL, policyEngine.URL)
	require.NoError(t, h.iWaitForEndpointToBeReady(readyEndpoint.URL))
}
