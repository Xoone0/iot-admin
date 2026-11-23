package simgen

import (
	"context"
	"strings"
	"testing"
)

func TestGenerateSafetyGuards(t *testing.T) {
	cfg := DefaultConfig()
	if _, err := Generate(context.Background(), nil, cfg); err == nil || !strings.Contains(err.Error(), "nil pool") {
		t.Fatalf("expected nil pool error, got %v", err)
	}
	cfg.Version = ""
	if _, err := Generate(context.Background(), nil, cfg); err == nil {
		t.Fatalf("expected error for empty version")
	}
}
