package mux

import "testing"

// TestRPCAction pins the first-two-segments reduction used for the "action" log
// field: real gRPC/Connect paths pass through unchanged, hand-mounted HTTP
// routes with dynamic trailing parameters collapse to "/{Service}/{Method}".
func TestRPCAction(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// ConnectRPC/gRPC — exactly two segments, unchanged.
		{"/agentx.v1.AgentService/ListProjects", "/agentx.v1.AgentService/ListProjects"},
		// Hand-mounted HTTP routes with dynamic trailing parameters.
		{"/integration.v1.AgentService/MCP/proj123", "/integration.v1.AgentService/MCP"},
		{"/integration.v1.AgentService/MCP/proj123/task/t456", "/integration.v1.AgentService/MCP"},
		{"/integration.v1.AgentService/A2A/pid789/.well-known/agent-card.json", "/integration.v1.AgentService/A2A"},
		{"/compute.v1.RunnerService/Files/download/agentx-node-linux-amd64", "/compute.v1.RunnerService/Files"},
		{"/compute.v1.RunnerService/Model/v1/messages", "/compute.v1.RunnerService/Model"},
		{"/compute.v1.RunnerService/Model/v1/messages/count_tokens", "/compute.v1.RunnerService/Model"},
		// Single-segment paths pass through (e.g. health/discovery).
		{"/healthz", "/healthz"},
		{"/.well-known/jwks.json", "/.well-known/jwks.json"},
		// Degenerate inputs.
		{"", ""},
		{"/", "/"},
	}
	for _, c := range cases {
		if got := rpcAction(c.in); got != c.want {
			t.Errorf("rpcAction(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
