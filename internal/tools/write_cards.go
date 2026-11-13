package tools

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sierikov/planka-mcp/internal/service"
)

type createCardInput struct {
	Board        string   `json:"board" jsonschema:"board to create the card on (ID, URL or name)"`
	List         string   `json:"list,omitempty" jsonschema:"list to put it in, by name (default: the board's first active list)"`
	Title        string   `json:"title" jsonschema:"card title"`
	Description  string   `json:"description,omitempty" jsonschema:"card description (markdown)"`
	Labels       []string `json:"labels,omitempty" jsonschema:"label names; a name the board lacks is an error unless create_labels is set"`
	CreateLabels bool     `json:"create_labels,omitempty" jsonschema:"create the labels the board lacks (default false, so a typo is not made into a new label)"`
	Assignees    []string `json:"assignees,omitempty" jsonschema:"people to assign, by name; \"me\" is you"`
	Due          string   `json:"due,omitempty" jsonschema:"due date: YYYY-MM-DD, YYYY-MM-DD HH:MM or RFC 3339"`
	Position     string   `json:"position,omitempty" jsonschema:"top (default) or bottom of the list"`
}

type moveCardInput struct {
	Card     string `json:"card" jsonschema:"card ID, card URL, or title (or part of it); a match in the description alone is refused"`
	Board    string `json:"board,omitempty" jsonschema:"board to look for the card on when card is a title (ID, URL or name); it is not the destination"`
	List     string `json:"list" jsonschema:"destination list, by name on the destination board, or archive"`
	ToBoard  string `json:"to_board,omitempty" jsonschema:"destination board when moving across boards (default: the card's own board)"`
	Position string `json:"position,omitempty" jsonschema:"top (default) or bottom of the list"`
}

type updateCardInput struct {
	Card            string   `json:"card" jsonschema:"card ID, card URL, or title (or part of it); a match in the description alone is refused"`
	Board           string   `json:"board,omitempty" jsonschema:"board to look on when card is a title (ID, URL or name)"`
	Title           *string  `json:"title,omitempty" jsonschema:"new title"`
	Description     *string  `json:"description,omitempty" jsonschema:"new description (markdown); it replaces the whole text, and an empty string clears it"`
	Due             *string  `json:"due,omitempty" jsonschema:"due date: YYYY-MM-DD, YYYY-MM-DD HH:MM or RFC 3339, or null/empty string to clear"`
	DueCompleted    *bool    `json:"due_completed,omitempty" jsonschema:"mark the due date done (true) or not done (false)"`
	AddLabels       []string `json:"add_labels,omitempty" jsonschema:"label names to add; a name the board lacks is an error unless create_labels is set"`
	CreateLabels    bool     `json:"create_labels,omitempty" jsonschema:"create the labels the board lacks (default false, so a typo is not made into a new label)"`
	RemoveLabels    []string `json:"remove_labels,omitempty" jsonschema:"label names to remove"`
	AddAssignees    []string `json:"add_assignees,omitempty" jsonschema:"people to assign, by name; \"me\" is you"`
	RemoveAssignees []string `json:"remove_assignees,omitempty" jsonschema:"people to unassign, by name"`
	Stopwatch       string   `json:"stopwatch,omitempty" jsonschema:"start, stop or reset the card's stopwatch"`
}

// UnmarshalJSON decodes the input as encoding/json does, except that an
// explicit "due": null becomes an empty Due, which clears the due date. Without
// it null would read as "left out", and the call would silently change nothing.
// It adds no method the schema sees, so the input schema stays the same.
func (in *updateCardInput) UnmarshalJSON(data []byte) error {
	type plain updateCardInput // drops this method, so Unmarshal does not recurse
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if due, ok := fields["due"]; ok && string(bytes.TrimSpace(due)) == "null" {
		p.Due = new("")
	}
	*in = updateCardInput(p)
	return nil
}

type deleteCardInput struct {
	Card  string `json:"card" jsonschema:"card ID, card URL, or title (or part of it); a match in the description alone is refused"`
	Board string `json:"board,omitempty" jsonschema:"board to look on when card is a title (ID, URL or name)"`
}

// writeOut is a card after a write. CreatedLabels names the labels the write had to create.
type writeOut struct {
	Card          cardLine `json:"card"`
	CreatedLabels []string `json:"created_labels,omitempty"`
}

func toWriteOut(r *service.WriteResult) writeOut {
	return writeOut{Card: toCardLine(r.Card), CreatedLabels: r.CreatedLabels}
}

// registerCardWriteTools adds the tools that create, change and delete cards.
func registerCardWriteTools(server *mcp.Server, cards CardService) {
	addTool(server, &mcp.Tool{
		Name:  "create_card",
		Title: "Create card",
		Description: "Use this to add a new card to a Planka list. " +
			"Returns the new card as a compact line, plus created_labels when create_labels made labels the board lacked.",
		Annotations: additive,
	}, createCard(cards))

	addTool(server, &mcp.Tool{
		Name:  "move_card",
		Title: "Move card",
		Description: "Use this to change a Planka card's status by moving it to another list, or to the archive with list=archive. " +
			"board only narrows the search for the card; to_board is the destination. Returns the card as a compact line.",
		Annotations: idempotent,
	}, moveCard(cards))

	addTool(server, &mcp.Tool{
		Name:  "update_card",
		Title: "Update card",
		Description: "Use this to edit a Planka card's title, description, due date, labels, assignees or stopwatch; fields you leave out stay as they are, " +
			"and description replaces the whole text, so read it with get_card first to append. " +
			"Returns the card as a compact line, plus created_labels when create_labels made labels the board lacked.",
		Annotations: idempotent,
	}, updateCard(cards))

	addTool(server, &mcp.Tool{
		Name:  "delete_card",
		Title: "Delete card",
		Description: "Use this only when a Planka card must be gone: it permanently deletes; to keep it, move it to the archive with move_card list=archive. " +
			"Returns the deleted card's id and title.",
		Annotations: destructive,
	}, deleteCard(cards))
}

func createCard(cards CardService) func(context.Context, *mcp.CallToolRequest, createCardInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in createCardInput) (*mcp.CallToolResult, any, error) {
		res, err := cards.Create(ctx, service.CreateCard{
			Board: in.Board, List: in.List, Title: in.Title, Description: in.Description,
			Labels: in.Labels, CreateLabels: in.CreateLabels, Assignees: in.Assignees, Due: in.Due, Position: in.Position,
		})
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(toWriteOut(res))
	}
}

func moveCard(cards CardService) func(context.Context, *mcp.CallToolRequest, moveCardInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in moveCardInput) (*mcp.CallToolResult, any, error) {
		res, err := cards.Move(ctx, service.MoveCard{
			Card: service.CardRef{Ref: in.Card, Board: in.Board}, List: in.List, ToBoard: in.ToBoard, Position: in.Position,
		})
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(toWriteOut(res))
	}
}

func updateCard(cards CardService) func(context.Context, *mcp.CallToolRequest, updateCardInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in updateCardInput) (*mcp.CallToolResult, any, error) {
		res, err := cards.Update(ctx, service.UpdateCard{
			Card:  service.CardRef{Ref: in.Card, Board: in.Board},
			Title: in.Title, Description: in.Description, Due: in.Due, DueCompleted: in.DueCompleted,
			AddLabels: in.AddLabels, RemoveLabels: in.RemoveLabels, CreateLabels: in.CreateLabels,
			AddAssignees: in.AddAssignees, RemoveAssignees: in.RemoveAssignees,
			Stopwatch: in.Stopwatch,
		})
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(toWriteOut(res))
	}
}

func deleteCard(cards CardService) func(context.Context, *mcp.CallToolRequest, deleteCardInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in deleteCardInput) (*mcp.CallToolResult, any, error) {
		card, err := cards.Delete(ctx, service.CardRef{Ref: in.Card, Board: in.Board})
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(deletedOut{Deleted: card.ID, Title: card.Title})
	}
}
