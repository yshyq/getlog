package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppliesDefaultsAndEnvironment(t *testing.T) {
	t.Setenv("PORTAL_USERNAME", "operator")
	t.Setenv("PORTAL_PASSWORD_HASH", "$2a$10$placeholder")
	t.Setenv("PORTAL_SESSION_KEY", "a-session-key-longer-than-thirty-two-bytes")
	path := writeConfig(t, `
auth: {}
services:
  - id: api
    displayName: API
    directory: api
discovery:
  mode: static
  staticNodes:
    - name: node-a.example.internal
      agentURL: http://127.0.0.1:8080
      nodeReady: true
      agentReady: true
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.Username != "operator" || cfg.Discovery.AgentPort != 8080 || cfg.FileList.MaxItems != 10000 {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestLoadRejectsDuplicateServiceDirectories(t *testing.T) {
	t.Setenv("PORTAL_USERNAME", "operator")
	t.Setenv("PORTAL_PASSWORD_HASH", "$2a$10$placeholder")
	t.Setenv("PORTAL_SESSION_KEY", "a-session-key-longer-than-thirty-two-bytes")
	path := writeConfig(t, `
services:
  - id: api
    displayName: API
    directory: shared
  - id: web
    displayName: Web
    directory: shared
discovery:
  mode: static
  staticNodes:
    - name: node-a.example.internal
      agentURL: http://127.0.0.1:8080
      nodeReady: true
      agentReady: true
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted duplicate service directories")
	}
}

func TestLoadRejectsUnsafeStaticAgentURL(t *testing.T) {
	t.Setenv("PORTAL_USERNAME", "operator")
	t.Setenv("PORTAL_PASSWORD_HASH", "$2a$10$placeholder")
	t.Setenv("PORTAL_SESSION_KEY", "a-session-key-longer-than-thirty-two-bytes")
	path := writeConfig(t, `
services:
  - id: api
    displayName: API
    directory: api
discovery:
  mode: static
  staticNodes:
    - name: node-a.example.internal
      agentURL: https://outside.example.invalid
      nodeReady: true
      agentReady: true
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted a non-HTTP static agent URL")
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "portal.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
