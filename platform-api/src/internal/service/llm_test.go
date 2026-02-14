package service

import (
	"testing"

	"platform-api/src/internal/constants"
	"platform-api/src/internal/model"
)

func TestPreserveUpstreamAuthValue(t *testing.T) {
	existing := &model.UpstreamConfig{
		Main: &model.UpstreamEndpoint{
			URL: "https://example.com",
			Auth: &model.UpstreamAuth{
				Type:   "api-key",
				Header: "Authorization",
				Value:  "secret",
			},
		},
	}

	t.Run("updated nil returns existing", func(t *testing.T) {
		out := preserveUpstreamAuthValue(existing, nil)
		if out != existing {
			t.Fatalf("expected existing config to be preserved")
		}
	})

	t.Run("existing nil returns updated", func(t *testing.T) {
		updated := &model.UpstreamConfig{Main: &model.UpstreamEndpoint{URL: "https://new.example"}}
		out := preserveUpstreamAuthValue(nil, updated)
		if out != updated {
			t.Fatalf("expected updated config to be returned")
		}
	})

	t.Run("missing main preserves existing", func(t *testing.T) {
		updated := &model.UpstreamConfig{}
		out := preserveUpstreamAuthValue(existing, updated)
		if out != existing {
			t.Fatalf("expected existing config to be preserved when main is nil")
		}
	})

	t.Run("missing auth preserves existing auth", func(t *testing.T) {
		updated := &model.UpstreamConfig{
			Main: &model.UpstreamEndpoint{URL: "https://example.com"},
		}
		out := preserveUpstreamAuthValue(existing, updated)
		if out.Main == nil || out.Main.Auth == nil {
			t.Fatalf("expected auth to be preserved")
		}
		if out.Main.Auth.Value != "secret" {
			t.Fatalf("expected auth value to be preserved")
		}
	})

	t.Run("empty auth value preserves existing", func(t *testing.T) {
		updated := &model.UpstreamConfig{
			Main: &model.UpstreamEndpoint{
				URL:  "https://example.com",
				Auth: &model.UpstreamAuth{Type: "api-key", Header: "Authorization", Value: ""},
			},
		}
		out := preserveUpstreamAuthValue(existing, updated)
		if out.Main.Auth.Value != "secret" {
			t.Fatalf("expected auth value to be preserved")
		}
	})
}

func TestMapUpstreamConfigToDTO_DoesNotExposeAuthValue(t *testing.T) {
	in := &model.UpstreamConfig{
		Main: &model.UpstreamEndpoint{
			URL: "https://example.com",
			Auth: &model.UpstreamAuth{
				Type:   "api-key",
				Header: "Authorization",
				Value:  "super-secret",
			},
		},
		Sandbox: &model.UpstreamEndpoint{
			URL: "https://sandbox.example.com",
			Auth: &model.UpstreamAuth{
				Type:   "api-key",
				Header: "Authorization",
				Value:  "sandbox-secret",
			},
		},
	}

	out := mapUpstreamConfigToDTO(in)
	if out.Main.Auth == nil {
		t.Fatalf("expected main auth to be present")
	}
	if out.Main.Auth.Value != nil && *out.Main.Auth.Value != "" {
		t.Fatalf("expected main auth value to be redacted")
	}
	if out.Sandbox == nil || out.Sandbox.Auth == nil {
		t.Fatalf("expected sandbox auth to be present")
	}
	if out.Sandbox.Auth.Value != nil && *out.Sandbox.Auth.Value != "" {
		t.Fatalf("expected sandbox auth value to be redacted")
	}
}

func TestValidateLLMResourceLimit(t *testing.T) {
	t.Run("below limit should pass", func(t *testing.T) {
		err := validateLLMResourceLimit(4, constants.MaxLLMProvidersPerOrganization, constants.ErrLLMProviderLimitReached)
		if err != nil {
			t.Fatalf("expected no error below limit, got: %v", err)
		}
	})

	t.Run("at limit should fail", func(t *testing.T) {
		err := validateLLMResourceLimit(5, constants.MaxLLMProvidersPerOrganization, constants.ErrLLMProviderLimitReached)
		if err != constants.ErrLLMProviderLimitReached {
			t.Fatalf("expected ErrLLMProviderLimitReached, got: %v", err)
		}
	})

	t.Run("above limit should fail", func(t *testing.T) {
		err := validateLLMResourceLimit(6, constants.MaxLLMProxiesPerOrganization, constants.ErrLLMProxyLimitReached)
		if err != constants.ErrLLMProxyLimitReached {
			t.Fatalf("expected ErrLLMProxyLimitReached, got: %v", err)
		}
	})
}
