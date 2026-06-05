package mcp

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// baseEnvVars are forwarded to every stdio subprocess regardless of the
// per-server whitelist. Without PATH the child cannot even locate its own
// executable (npx, docker, ...); HOME is needed by many runtimes (npm cache,
// config). These are not secrets, so forwarding them is safe — unlike dumping
// the parent's whole environment, which could leak OPENAI_API_KEY, cloud
// credentials, etc. into a third-party server process.
var baseEnvVars = []string{"PATH", "HOME"}

// newTransport builds the SDK transport for a server entry, dispatching on the
// configured transport kind. The config is assumed already validated by
// LoadConfig.
func newTransport(cfg ServerConfig) (mcp.Transport, error) {
	switch cfg.Transport {
	case TransportStdio:
		cmd := exec.Command(cfg.Command, cfg.Args...)
		cmd.Env = forwardEnv(cfg.Env)
		return &mcp.CommandTransport{Command: cmd}, nil

	case TransportHTTP:
		// Prefer the modern Streamable HTTP transport (2025 spec); it supports
		// reconnects. Custom headers (e.g. Authorization) are injected via a
		// header-setting RoundTripper, since the transport itself exposes only
		// an *http.Client.
		t := &mcp.StreamableClientTransport{Endpoint: cfg.URL}
		if len(cfg.Headers) > 0 {
			t.HTTPClient = &http.Client{
				Transport: &headerRoundTripper{base: http.DefaultTransport, headers: cfg.Headers},
			}
		}
		return t, nil

	default:
		// Unreachable if LoadConfig validated, but keep the guard honest.
		return nil, fmt.Errorf("unknown transport %q", cfg.Transport)
	}
}

// forwardEnv builds the child process environment: a safe baseline plus the
// values of the whitelisted names, pulled from THIS process's environment.
// Names that are not set in the parent are skipped silently.
func forwardEnv(names []string) []string {
	env := make([]string, 0, len(baseEnvVars)+len(names))
	for _, k := range baseEnvVars {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	for _, k := range names {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// headerRoundTripper sets fixed headers on every outgoing request before
// delegating to the base transport.
type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range h.headers {
		req.Header.Set(k, v)
	}
	return h.base.RoundTrip(req)
}
