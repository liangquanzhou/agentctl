package engine

import "testing"

func TestBuildDesiredMCPServers_AppliesPerAgentOverrides(t *testing.T) {
	registry := map[string]map[string]any{
		"servers.json": {
			"servers": map[string]any{
				"multi-model-mcp": map[string]any{
					"command": "node",
					"args":    []any{"server.js"},
					"env": map[string]any{
						"BASE": "1",
					},
					"envRef": []any{"SHARED_TOKEN"},
				},
			},
		},
		"profiles.json": {
			"agents": map[string]any{
				"codex": map[string]any{
					"servers": []any{"multi-model-mcp"},
					"overrides": map[string]any{
						"multi-model-mcp": map[string]any{
							"env": map[string]any{
								"CALLER_AGENT": "codex",
							},
						},
					},
				},
				"claude-code": map[string]any{
					"servers": []any{"multi-model-mcp"},
					"overrides": map[string]any{
						"multi-model-mcp": map[string]any{
							"env": map[string]any{
								"CALLER_AGENT": "claude-code",
							},
						},
					},
				},
			},
			"servers": map[string]any{},
		},
		"compat.json": {
			"legacy_enabled": false,
		},
	}

	envValues := map[string]string{
		"SHARED_TOKEN": "secret",
	}

	codexDesired := buildDesiredMCPServers("codex", registry, envValues)
	claudeDesired := buildDesiredMCPServers("claude-code", registry, envValues)

	codexServer, ok := codexDesired["multi-model-mcp"].(map[string]any)
	if !ok {
		t.Fatalf("expected codex multi-model-mcp entry, got %#v", codexDesired["multi-model-mcp"])
	}
	claudeServer, ok := claudeDesired["multi-model-mcp"].(map[string]any)
	if !ok {
		t.Fatalf("expected claude multi-model-mcp entry, got %#v", claudeDesired["multi-model-mcp"])
	}

	codexEnv, ok := codexServer["env"].(map[string]string)
	if !ok {
		t.Fatalf("expected codex env map[string]string, got %#v", codexServer["env"])
	}
	claudeEnv, ok := claudeServer["env"].(map[string]string)
	if !ok {
		t.Fatalf("expected claude env map[string]string, got %#v", claudeServer["env"])
	}

	if got := codexEnv["BASE"]; got != "1" {
		t.Fatalf("expected base env to be preserved for codex, got %q", got)
	}
	if got := codexEnv["SHARED_TOKEN"]; got != "secret" {
		t.Fatalf("expected envRef to resolve for codex, got %q", got)
	}
	if got := codexEnv["CALLER_AGENT"]; got != "codex" {
		t.Fatalf("expected codex override env, got %q", got)
	}
	if got := claudeEnv["CALLER_AGENT"]; got != "claude-code" {
		t.Fatalf("expected claude override env, got %q", got)
	}
	if got := codexEnv["CALLER_AGENT"]; got == claudeEnv["CALLER_AGENT"] {
		t.Fatalf("expected per-agent overrides to stay isolated, got %q for both", got)
	}

	serversFile := registry["servers.json"]
	servers := serversFile["servers"].(map[string]any)
	base := servers["multi-model-mcp"].(map[string]any)
	baseEnv := base["env"].(map[string]any)
	if _, exists := baseEnv["CALLER_AGENT"]; exists {
		t.Fatalf("expected base server spec to remain unchanged, got %#v", baseEnv)
	}
}

func TestBuildDesiredOpencode_AppliesPerAgentOverrides(t *testing.T) {
	registry := map[string]map[string]any{
		"servers.json": {
			"servers": map[string]any{
				"multi-model-mcp": map[string]any{
					"command": "node",
					"args":    []any{"server.js"},
				},
			},
		},
		"profiles.json": {
			"agents": map[string]any{
				"opencode": map[string]any{
					"servers": []any{"multi-model-mcp"},
					"overrides": map[string]any{
						"multi-model-mcp": map[string]any{
							"env": map[string]any{
								"CALLER_AGENT": "opencode",
							},
						},
					},
				},
			},
			"servers": map[string]any{},
		},
		"compat.json": {
			"legacy_enabled": false,
		},
	}

	desired := buildDesiredOpencode("opencode", registry, map[string]string{})
	server, ok := desired["multi-model-mcp"].(map[string]any)
	if !ok {
		t.Fatalf("expected opencode multi-model-mcp entry, got %#v", desired["multi-model-mcp"])
	}

	env, ok := server["environment"].(map[string]string)
	if !ok {
		t.Fatalf("expected opencode environment map[string]string, got %#v", server["environment"])
	}
	if got := env["CALLER_AGENT"]; got != "opencode" {
		t.Fatalf("expected opencode override env, got %q", got)
	}
}
