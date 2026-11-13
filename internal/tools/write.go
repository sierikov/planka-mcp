package tools

import "github.com/modelcontextprotocol/go-sdk/mcp"

// Annotations of the write tools. A create or an add only adds; an update
// ends in the same state when repeated; a delete is destructive. Every tool
// is closed-world: it touches only the user's own Planka.
var (
	additive    = &mcp.ToolAnnotations{DestructiveHint: new(false), OpenWorldHint: new(false)}
	idempotent  = &mcp.ToolAnnotations{IdempotentHint: true, OpenWorldHint: new(false)}
	destructive = &mcp.ToolAnnotations{DestructiveHint: new(true), OpenWorldHint: new(false)}
)

// registerWriteTools adds the tools that change Planka.
func registerWriteTools(server *mcp.Server, deps Deps) {
	registerCardWriteTools(server, deps.Cards)
	registerChecklistWriteTools(server, deps.Comments, deps.Tasks)
}

// deletedOut confirms a delete. Title is set for cards only.
type deletedOut struct {
	Deleted string `json:"deleted"`
	Title   string `json:"title,omitempty"`
}
