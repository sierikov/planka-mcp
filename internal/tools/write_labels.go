package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sierikov/planka-mcp/internal/service"
)

type createLabelInput struct {
	Board string  `json:"board" jsonschema:"board ID, URL or name"`
	Name  string  `json:"name" jsonschema:"the new label's name; it must differ from the board's other labels"`
	Color *string `json:"color,omitempty" jsonschema:"one of Planka's label colours (default: one picked from the name)"`
}

type updateLabelInput struct {
	Board string  `json:"board" jsonschema:"board ID, URL or name"`
	Label string  `json:"label" jsonschema:"the label to change, by exact name or ID"`
	Name  *string `json:"name,omitempty" jsonschema:"new name; it must differ from the board's other labels"`
	Color *string `json:"color,omitempty" jsonschema:"new colour, one of Planka's label colours"`
}

type deleteLabelInput struct {
	Board     string `json:"board" jsonschema:"board ID, URL or name"`
	Label     string `json:"label" jsonschema:"the label to delete, by exact name or ID"`
	FromCards bool   `json:"from_cards,omitempty" jsonschema:"also take it off the cards that have it (default false: a label in use is refused)"`
}

// labelOut is a label after a write. CardsTotal counts the cards in the
// board's lists that have it.
type labelOut struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Color      string `json:"color"`
	CardsTotal int    `json:"cards_total"`
}

// newLabelOut is a label create_label made.
type newLabelOut struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// deletedLabelOut confirms a deleted label and lists the cards it was taken
// off, so it can be put back; More counts the cards left out.
type deletedLabelOut struct {
	Deleted string     `json:"deleted"`
	Name    string     `json:"name"`
	Color   string     `json:"color"`
	Cards   []cardLine `json:"cards,omitempty"`
	More    int        `json:"more,omitzero"`
	Hint    string     `json:"hint,omitempty"`
}

// registerLabelTools adds the tools that create, change and delete a board's
// labels. create_card and update_card also create labels, with create_labels.
func registerLabelTools(server *mcp.Server, labels LabelService) {
	addTool(server, &mcp.Tool{
		Name:  "create_label",
		Title: "Create label",
		Description: "Use this to add a label to a Planka board without putting it on a card. " +
			"To create a label while labelling a card, use create_card or update_card with create_labels instead. " +
			"Returns the label (id, name, color).",
		Annotations: additive,
		InputSchema: inputSchema[createLabelInput](map[string][]string{"color": service.LabelColors}),
	}, createLabel(labels))

	addTool(server, &mcp.Tool{
		Name:  "update_label",
		Title: "Update label",
		Description: "Use this to rename or recolour a label on a Planka board; every card with it shows the change. " +
			"Fields you leave out stay as they are. Returns the label (id, name, color) and cards_total, the cards on the board's lists that have it.",
		Annotations: idempotent,
		InputSchema: inputSchema[updateLabelInput](map[string][]string{"color": service.LabelColors}),
	}, updateLabel(labels))

	addTool(server, &mcp.Tool{
		Name:  "delete_label",
		Title: "Delete label",
		Description: "Use this only when a label on a Planka board must go. Planka takes it off every card that has it, archived ones too, " +
			"so a label in use is refused unless from_cards=true. To take a label off one card, use update_card remove_labels. " +
			"Returns the deleted label and the cards it was taken off.",
		Annotations: destructive,
	}, deleteLabel(labels))
}

func createLabel(labels LabelService) func(context.Context, *mcp.CallToolRequest, createLabelInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in createLabelInput) (*mcp.CallToolResult, any, error) {
		res, err := labels.Create(ctx, service.CreateLabel{Board: in.Board, Name: in.Name, Color: in.Color})
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(newLabelOut{ID: res.ID, Name: res.Name, Color: res.Color})
	}
}

func updateLabel(labels LabelService) func(context.Context, *mcp.CallToolRequest, updateLabelInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in updateLabelInput) (*mcp.CallToolResult, any, error) {
		res, err := labels.Update(ctx, service.UpdateLabel{Board: in.Board, Label: in.Label, Name: in.Name, Color: in.Color})
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(labelOut{ID: res.ID, Name: res.Name, Color: res.Color, CardsTotal: res.Cards})
	}
}

func deleteLabel(labels LabelService) func(context.Context, *mcp.CallToolRequest, deleteLabelInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in deleteLabelInput) (*mcp.CallToolResult, any, error) {
		res, err := labels.Delete(ctx, in.Board, in.Label, in.FromCards)
		if err != nil {
			return errorResult(err)
		}
		out := deletedLabelOut{Deleted: res.ID, Name: res.Name, Color: res.Color, Cards: toCardLines(res.Cards), More: res.More}
		if res.Partial {
			out.Hint = "Planka could not list every archived card, so more of them may have had this label"
		}
		return jsonResult(out)
	}
}
