package tools

import (
	"context"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sierikov/planka-mcp/internal/service"
)

type createListInput struct {
	Board  string `json:"board" jsonschema:"board to add the list to (ID, URL or name)"`
	Name   string `json:"name" jsonschema:"the new list's name; it must differ from the board's other lists"`
	After  string `json:"after,omitempty" jsonschema:"put it right after this list (exact name or ID); without after or before it goes last"`
	Before string `json:"before,omitempty" jsonschema:"put it right before this list (exact name or ID)"`
	Closed bool   `json:"closed,omitempty" jsonschema:"make it a closed list, whose cards count as done, like a Done list (default false)"`
	Color  string `json:"color,omitempty" jsonschema:"the list's colour (default none)"`
}

type updateListInput struct {
	Board        string  `json:"board" jsonschema:"board the list is on (ID, URL or name)"`
	List         string  `json:"list" jsonschema:"the list to change, by exact name or ID"`
	Name         *string `json:"name,omitempty" jsonschema:"new name"`
	Color        string  `json:"color,omitempty" jsonschema:"new colour, or none to clear it"`
	Closed       *bool   `json:"closed,omitempty" jsonschema:"true makes it a closed list, whose cards count as done; false makes it active"`
	After        string  `json:"after,omitempty" jsonschema:"move it right after this list (exact name or ID)"`
	Before       string  `json:"before,omitempty" jsonschema:"move it right before this list (exact name or ID)"`
	SortCards    string  `json:"sort_cards,omitempty" jsonschema:"reorder the list's cards by name, due date or creation date"`
	SortOrder    string  `json:"sort_order,omitempty" jsonschema:"with sort_cards: asc (default) or desc"`
	ArchiveCards bool    `json:"archive_cards,omitempty" jsonschema:"move every card in the list to the board's archive; only for a closed list (closed=true in the same call closes it first)"`
}

type deleteListInput struct {
	Board string `json:"board" jsonschema:"board the list is on (ID, URL or name)"`
	List  string `json:"list" jsonschema:"the list to delete, by exact name or ID"`
}

// columnOut is a list as a list write leaves it. CardsTotal counts its cards;
// get_board lists them.
type columnOut struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Color      string `json:"color,omitempty"`
	Closed     bool   `json:"closed,omitempty"`
	CardsTotal int    `json:"cards_total"`
}

// listResultOut is a list after a write. ListOrder names the board's lists in
// order. Archived counts the cards archive_cards moved.
type listResultOut struct {
	List      columnOut `json:"list"`
	ListOrder []string  `json:"list_order"`
	Archived  int       `json:"archived,omitzero"`
}

// deletedListOut confirms a deleted list.
type deletedListOut struct {
	Deleted      string `json:"deleted"`
	Name         string `json:"name"`
	CardsToTrash int    `json:"cards_moved_to_trash"`
}

// registerListWriteTools adds the tools that change a board's lists (columns).
func registerListWriteTools(server *mcp.Server, lists ListService) {
	addTool(server, &mcp.Tool{
		Name:  "create_list",
		Title: "Create list",
		Description: "Use this to add a list (a column) to a Planka board, for example a new stage of a workflow. " +
			"It goes last unless after or before names a neighbour; closed=true makes it a done list. " +
			"To rename, reorder or recolour an existing list, use update_list instead. " +
			"Returns the new list (id, name, color, closed, cards_total) and list_order, the board's list names in order.",
		Annotations: additive,
		InputSchema: inputSchema[createListInput](map[string][]string{"color": service.ListColors}),
	}, createList(lists))

	addTool(server, &mcp.Tool{
		Name:  "update_list",
		Title: "Update list",
		Description: "Use this to change a Planka list (a column): rename, recolour or move it, make it closed or active, " +
			"sort its cards by name, due date or creation date, or archive every card in a closed list. " +
			"Fields you leave out stay as they are. To move single cards, use move_card. " +
			"Returns the list and list_order, the board's list names in order, plus archived when archive_cards moved cards.",
		Annotations: idempotent,
		InputSchema: inputSchema[updateListInput](map[string][]string{
			"color":      append(slices.Clone(service.ListColors), service.NoColor),
			"sort_cards": service.ListSorts,
			"sort_order": service.SortOrders,
		}),
	}, updateList(lists))

	addTool(server, &mcp.Tool{
		Name:  "delete_list",
		Title: "Delete list",
		Description: "Use this only when a Planka list (a column) must go. Planka moves every card in it to the board's Trash, " +
			"where they can be restored in Planka's web UI; to keep them on the board, move them first with move_card, " +
			"or archive them with update_list archive_cards. Returns the deleted list's id and name and how many cards went to Trash.",
		Annotations: destructive,
	}, deleteList(lists))
}

func createList(lists ListService) func(context.Context, *mcp.CallToolRequest, createListInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in createListInput) (*mcp.CallToolResult, any, error) {
		res, err := lists.Create(ctx, service.CreateList{
			Board: in.Board, Name: in.Name, Place: service.ListPlace{After: in.After, Before: in.Before},
			Closed: in.Closed, Color: in.Color,
		})
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(toListResultOut(res))
	}
}

func updateList(lists ListService) func(context.Context, *mcp.CallToolRequest, updateListInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in updateListInput) (*mcp.CallToolResult, any, error) {
		res, err := lists.Update(ctx, service.UpdateList{
			Board: in.Board, List: in.List, Name: in.Name, Color: in.Color, Closed: in.Closed,
			Place:  service.ListPlace{After: in.After, Before: in.Before},
			SortBy: in.SortCards, SortOrder: in.SortOrder, ArchiveCards: in.ArchiveCards,
		})
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(toListResultOut(res))
	}
}

func deleteList(lists ListService) func(context.Context, *mcp.CallToolRequest, deleteListInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in deleteListInput) (*mcp.CallToolResult, any, error) {
		res, err := lists.Delete(ctx, in.Board, in.List)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(deletedListOut{Deleted: res.ID, Name: res.Name, CardsToTrash: res.CardsToTrash})
	}
}

func toListResultOut(r *service.ListResult) listResultOut {
	c := r.List
	return listResultOut{
		List:      columnOut{ID: c.ID, Name: c.Name, Color: c.Color, Closed: c.Closed, CardsTotal: c.Cards},
		ListOrder: r.Order,
		Archived:  r.Archived,
	}
}
