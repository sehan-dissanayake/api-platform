package service

import (
	"testing"

	"platform-api/src/internal/model"
)

func TestMapModelAuthToAPI_NormalizesApiKeyType(t *testing.T) {
	auth := &model.UpstreamAuth{Type: "apiKey", Header: "Authorization", Value: "secret"}

	out := mapModelAuthToAPI(auth)
	if out == nil || out.Type == nil {
		t.Fatal("expected auth type to be present")
	}
	if *out.Type != "api-key" {
		t.Fatalf("expected auth type to be api-key, got %q", *out.Type)
	}
}
