package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sierikov/planka-mcp/internal/service"
)

type setCustomFieldsInput struct {
	Card   string            `json:"card" jsonschema:"card ID, card URL, or title (or part of it); a match in the description alone is refused"`
	Board  string            `json:"board,omitempty" jsonschema:"board to look on when card is a title (ID, URL or name)"`
	Fields map[string]string `json:"fields" jsonschema:"field name (or \"Group / Field\" when the name is in several groups) to its new value; an empty value clears the field"`
}

// customFieldsOut is a card's custom fields after a write.
type customFieldsOut struct {
	CustomFields []customFieldGroupOut `json:"custom_fields"`
}

// registerCustomFieldTools adds the tool that sets a card's custom fields.
func registerCustomFieldTools(server *mcp.Server, fields CustomFieldService) {
	addTool(server, &mcp.Tool{
		Name:  "set_custom_fields",
		Title: "Set custom fields",
		Description: "Use this to set or clear the values of a Planka card's custom fields, several in one call. " +
			"The fields must exist on the card or its board; get_card lists them, the empty ones under unset. Values are plain text. " +
			"Returns the card's custom fields.",
		Annotations: idempotent,
	}, setCustomFields(fields))
}

func setCustomFields(fields CustomFieldService) func(context.Context, *mcp.CallToolRequest, setCustomFieldsInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in setCustomFieldsInput) (*mcp.CallToolResult, any, error) {
		groups, err := fields.Set(ctx, service.CardRef{Ref: in.Card, Board: in.Board}, in.Fields)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(customFieldsOut{CustomFields: toCustomFieldGroups(groups)})
	}
}
