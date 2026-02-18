package main

import (
	"testing"

	"github.com/mosaxiv/clawlet/config"
)

func TestValidateGatewayBindPolicy_LocalhostAllowed(t *testing.T) {
	cfg := config.GatewayConfig{Listen: "127.0.0.1:18790"}
	if err := validateGatewayBindPolicy(cfg); err != nil {
		t.Fatalf("expected localhost bind allowed, got: %v", err)
	}
}

func TestValidateGatewayBindPolicy_PublicBindRejectedByDefault(t *testing.T) {
	cfg := config.GatewayConfig{Listen: "0.0.0.0:18790"}
	if err := validateGatewayBindPolicy(cfg); err == nil {
		t.Fatalf("expected public bind to be rejected by default")
	}
}

func TestValidateGatewayBindPolicy_PublicBindAllowedWhenExplicitlyEnabled(t *testing.T) {
	cfg := config.GatewayConfig{
		Listen:          "0.0.0.0:18790",
		AllowPublicBind: true,
	}
	if err := validateGatewayBindPolicy(cfg); err != nil {
		t.Fatalf("expected explicit public bind allow, got: %v", err)
	}
}
