package transport

import (
	"testing"
)

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		name        string
		endpoint    string
		wantType    EndpointType
		wantURL     string
		wantCommand string
		wantErr     bool
	}{
		{
			name:     "websocket ws://",
			endpoint: "ws://localhost:8080/mcp",
			wantType: EndpointWebSocket,
			wantURL:  "ws://localhost:8080/mcp",
		},
		{
			name:     "websocket wss://",
			endpoint: "wss://mcp.example.com/v1",
			wantType: EndpointWebSocket,
			wantURL:  "wss://mcp.example.com/v1",
		},
		{
			name:        "stdio simple",
			endpoint:    "stdio:///usr/local/bin/mcp-server",
			wantType:    EndpointStdio,
			wantCommand: "/usr/local/bin/mcp-server",
		},
		{
			name:        "stdio with args",
			endpoint:    "stdio:///usr/bin/python?arg=-m&arg=mcp_server",
			wantType:    EndpointStdio,
			wantCommand: "/usr/bin/python",
		},
		{
			name:     "empty endpoint",
			endpoint: "",
			wantErr:  true,
		},
		{
			name:     "unsupported scheme",
			endpoint: "http://localhost:8080",
			wantErr:  true,
		},
		{
			name:     "invalid URL",
			endpoint: "://invalid",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := ParseEndpoint(tt.endpoint)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseEndpoint(%q) expected error, got nil", tt.endpoint)
				}
				return
			}

			if err != nil {
				t.Errorf("ParseEndpoint(%q) unexpected error: %v", tt.endpoint, err)
				return
			}

			if parsed.Type != tt.wantType {
				t.Errorf("ParseEndpoint(%q) type = %v, want %v", tt.endpoint, parsed.Type, tt.wantType)
			}

			if tt.wantType == EndpointWebSocket && parsed.URL != tt.wantURL {
				t.Errorf("ParseEndpoint(%q) URL = %q, want %q", tt.endpoint, parsed.URL, tt.wantURL)
			}

			if tt.wantType == EndpointStdio {
				if parsed.StdioCfg == nil {
					t.Errorf("ParseEndpoint(%q) StdioCfg is nil", tt.endpoint)
				} else if parsed.StdioCfg.Command != tt.wantCommand {
					t.Errorf("ParseEndpoint(%q) Command = %q, want %q", tt.endpoint, parsed.StdioCfg.Command, tt.wantCommand)
				}
			}
		})
	}
}

func TestParseStdioEndpoint(t *testing.T) {
	tests := []struct {
		name        string
		endpoint    string
		wantCommand string
		wantArgs    []string
		wantEnv     []string
		wantWorkDir string
		wantErr     bool
	}{
		{
			name:        "simple binary",
			endpoint:    "stdio:///usr/local/bin/mcp-server",
			wantCommand: "/usr/local/bin/mcp-server",
		},
		{
			name:        "with args",
			endpoint:    "stdio:///usr/bin/node?arg=server.js&arg=--port&arg=3000",
			wantCommand: "/usr/bin/node",
			wantArgs:    []string{"server.js", "--port", "3000"},
		},
		{
			name:        "with env",
			endpoint:    "stdio:///app/server?env=DEBUG=true&env=LOG_LEVEL=info",
			wantCommand: "/app/server",
			wantEnv:     []string{"DEBUG=true", "LOG_LEVEL=info"},
		},
		{
			name:        "with workdir",
			endpoint:    "stdio:///app/server?workdir=/var/app",
			wantCommand: "/app/server",
			wantWorkDir: "/var/app",
		},
		{
			name:        "full config",
			endpoint:    "stdio:///usr/bin/python?arg=-m&arg=server&env=PYTHONPATH=/lib&workdir=/app",
			wantCommand: "/usr/bin/python",
			wantArgs:    []string{"-m", "server"},
			wantEnv:     []string{"PYTHONPATH=/lib"},
			wantWorkDir: "/app",
		},
		{
			name:     "wrong scheme",
			endpoint: "ws://localhost:8080",
			wantErr:  true,
		},
		{
			name:     "missing path",
			endpoint: "stdio://",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseStdioEndpoint(tt.endpoint)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseStdioEndpoint(%q) expected error, got nil", tt.endpoint)
				}
				return
			}

			if err != nil {
				t.Errorf("ParseStdioEndpoint(%q) unexpected error: %v", tt.endpoint, err)
				return
			}

			if cfg.Command != tt.wantCommand {
				t.Errorf("Command = %q, want %q", cfg.Command, tt.wantCommand)
			}

			if !slicesEqual(cfg.Args, tt.wantArgs) {
				t.Errorf("Args = %v, want %v", cfg.Args, tt.wantArgs)
			}

			if !slicesEqual(cfg.Env, tt.wantEnv) {
				t.Errorf("Env = %v, want %v", cfg.Env, tt.wantEnv)
			}

			if cfg.WorkDir != tt.wantWorkDir {
				t.Errorf("WorkDir = %q, want %q", cfg.WorkDir, tt.wantWorkDir)
			}
		})
	}
}

func TestBuildStdioEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		binary  string
		args    []string
		env     []string
		workdir string
		want    string
	}{
		{
			name:   "simple",
			binary: "/usr/bin/server",
			want:   "stdio:///usr/bin/server",
		},
		{
			name:   "with args",
			binary: "/usr/bin/node",
			args:   []string{"server.js"},
			want:   "stdio:///usr/bin/node?arg=server.js",
		},
		{
			name:    "with workdir",
			binary:  "/app/server",
			workdir: "/var/app",
			want:    "stdio:///app/server?workdir=%2Fvar%2Fapp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildStdioEndpoint(tt.binary, tt.args, tt.env, tt.workdir)
			// Parse both to compare (URL encoding may vary)
			gotCfg, err := ParseStdioEndpoint(got)
			if err != nil {
				t.Fatalf("BuildStdioEndpoint result couldn't be parsed: %v", err)
			}

			if gotCfg.Command != tt.binary {
				t.Errorf("Command = %q, want %q", gotCfg.Command, tt.binary)
			}
		})
	}
}

func TestEndpointTypeString(t *testing.T) {
	tests := []struct {
		t    EndpointType
		want string
	}{
		{EndpointWebSocket, "websocket"},
		{EndpointStdio, "stdio"},
		{EndpointType(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.t.String(); got != tt.want {
			t.Errorf("EndpointType(%d).String() = %q, want %q", tt.t, got, tt.want)
		}
	}
}

func TestIsStdioEndpoint(t *testing.T) {
	tests := []struct {
		endpoint string
		want     bool
	}{
		{"stdio:///bin/server", true},
		{"ws://localhost:8080", false},
		{"wss://example.com", false},
		{"http://example.com", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := IsStdioEndpoint(tt.endpoint); got != tt.want {
			t.Errorf("IsStdioEndpoint(%q) = %v, want %v", tt.endpoint, got, tt.want)
		}
	}
}

// slicesEqual compares two string slices for equality.
func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
