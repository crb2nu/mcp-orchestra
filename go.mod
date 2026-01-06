module gitlab.flexinfer.ai/services/mcp-orchestra

go 1.24.0

require (
	github.com/google/uuid v1.6.0
	github.com/spf13/cobra v1.9.1
	gitlab.flexinfer.ai/libs/fi-mcp-kit v0.0.0
	gitlab.flexinfer.ai/libs/mcp-go v0.1.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.6 // indirect
	golang.org/x/net v0.26.0 // indirect
)

// Use local mcp-go with pool package (until v0.2.0 is published)
replace gitlab.flexinfer.ai/libs/mcp-go => ../../libs/mcp-go

// Use local fi-mcp-kit for registry/schema compatibility
replace gitlab.flexinfer.ai/libs/fi-mcp-kit => ../../libs/fi-mcp-kit
