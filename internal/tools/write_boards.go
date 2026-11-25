package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sierikov/planka-mcp/internal/service"
)

type createBoardInput struct {
	Project    string   `json:"project" jsonschema:"project ID or name; list_boards shows them"`
	Name       string   `json:"name" jsonschema:"the new board's name; it must differ from the project's other boards"`
	Lists      []string `json:"lists,omitempty" jsonschema:"names of the board's lists (columns), in order"`
	ClosedList string   `json:"closed_list,omitempty" jsonschema:"name of a closed list for finished cards, added last (for example Done)"`
}

type updateBoardInput struct {
	Board string `json:"board" jsonschema:"board ID, URL or name"`
	Name  string `json:"name" jsonschema:"new name; it must differ from the project's other boards"`
}

type deleteBoardInput struct {
	Board     string `json:"board" jsonschema:"board ID, URL or name"`
	WithCards bool   `json:"with_cards,omitempty" jsonschema:"also delete the cards still on it, archived ones included (default false: a board with cards is refused)"`
}

type addMemberInput struct {
	Board      string `json:"board" jsonschema:"board ID, URL or name"`
	Person     string `json:"person" jsonschema:"username, full name or email of a Planka user, or me"`
	Role       string `json:"role" jsonschema:"editor (can change cards) or viewer (read only)"`
	CanComment *bool  `json:"can_comment,omitempty" jsonschema:"let a viewer comment (default false); editors always can"`
}

type updateMemberInput struct {
	Board      string  `json:"board" jsonschema:"board ID, URL or name"`
	Person     string  `json:"person" jsonschema:"a member of the board: username, full name, or me"`
	Role       *string `json:"role,omitempty" jsonschema:"editor or viewer"`
	CanComment *bool   `json:"can_comment,omitempty" jsonschema:"let a viewer comment"`
}

type removeMemberInput struct {
	Board  string `json:"board" jsonschema:"board ID, URL or name"`
	Person string `json:"person" jsonschema:"a member of the board: username, full name, or me"`
}

// renamedBoardOut is a board after a rename.
type renamedBoardOut struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Project string `json:"project"`
}

// deletedBoardOut confirms a deleted board. CardsTotal counts the cards
// deleted with it; CardsAtLeast says the count is a lower bound, because
// Planka 2.1.1 shows only the archive's first page.
type deletedBoardOut struct {
	Deleted      string `json:"deleted"`
	Name         string `json:"name"`
	Project      string `json:"project,omitempty"`
	CardsTotal   int    `json:"cards_total"`
	CardsAtLeast bool   `json:"cards_at_least,omitzero"`
}

// memberOut is a person on a board.
type memberOut struct {
	Name       string `json:"name"`
	Username   string `json:"username,omitempty"`
	Role       string `json:"role"`
	CanComment bool   `json:"can_comment"`
}

// removedMemberOut confirms a removed member.
type removedMemberOut struct {
	Removed string `json:"removed"`
	Role    string `json:"role"`
}

// registerBoardTools adds the tools that create, rename and delete boards and
// manage their members. Planka allows them to project managers only.
func registerBoardTools(server *mcp.Server, boards BoardService) {
	addTool(server, &mcp.Tool{
		Name:  "create_board",
		Title: "Create board",
		Description: "Use this to add a board to a Planka project, with its lists in one call. Only a project manager can. " +
			"Returns the board as get_board shows it.",
		Annotations: additive,
	}, createBoard(boards))

	addTool(server, &mcp.Tool{
		Name:        "update_board",
		Title:       "Update board",
		Description: "Use this to rename a Planka board. Only a project manager can. Returns the board's id, name and project.",
		Annotations: idempotent,
	}, updateBoard(boards))

	addTool(server, &mcp.Tool{
		Name:  "delete_board",
		Title: "Delete board",
		Description: "Use this only when a whole Planka board must go: its lists and cards are deleted with it, for good. " +
			"A board with cards is refused unless with_cards=true. Only a project manager can. Returns the deleted board and how many cards went with it.",
		Annotations: destructive,
	}, deleteBoard(boards))

	addTool(server, &mcp.Tool{
		Name:  "add_board_member",
		Title: "Add board member",
		Description: "Use this to give someone access to a Planka board, as editor or viewer. " +
			"Only a project manager can, and adding anyone but yourself (person=me) needs a Planka admin or project owner. Returns the member.",
		Annotations: additive,
		InputSchema: inputSchema[addMemberInput](map[string][]string{"role": service.MemberRoles}),
	}, addMember(boards))

	addTool(server, &mcp.Tool{
		Name:        "update_board_member",
		Title:       "Update board member",
		Description: "Use this to change a Planka board member's role, or whether a viewer may comment. Only a project manager can. Returns the member.",
		Annotations: idempotent,
		InputSchema: inputSchema[updateMemberInput](map[string][]string{"role": service.MemberRoles}),
	}, updateMember(boards))

	addTool(server, &mcp.Tool{
		Name:  "remove_board_member",
		Title: "Remove board member",
		Description: "Use this to take someone's access to a Planka board away. Only a project manager can, except that anyone can remove themselves (person=me), " +
			"which ends their own access. Returns who was removed and their role.",
		Annotations: destructive,
	}, removeMember(boards))
}

func createBoard(boards BoardService) func(context.Context, *mcp.CallToolRequest, createBoardInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in createBoardInput) (*mcp.CallToolResult, any, error) {
		b, err := boards.Create(ctx, service.CreateBoard{Project: in.Project, Name: in.Name, Lists: in.Lists, ClosedList: in.ClosedList})
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(toBoardOut(b, func(service.List) int { return boardListCards }))
	}
}

func updateBoard(boards BoardService) func(context.Context, *mcp.CallToolRequest, updateBoardInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in updateBoardInput) (*mcp.CallToolResult, any, error) {
		b, err := boards.Rename(ctx, in.Board, in.Name)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(renamedBoardOut{ID: b.ID, Name: b.Name, Project: b.Project})
	}
}

func deleteBoard(boards BoardService) func(context.Context, *mcp.CallToolRequest, deleteBoardInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in deleteBoardInput) (*mcp.CallToolResult, any, error) {
		d, err := boards.Delete(ctx, in.Board, in.WithCards)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(deletedBoardOut{Deleted: d.ID, Name: d.Name, Project: d.Project, CardsTotal: d.Cards, CardsAtLeast: d.AtLeast})
	}
}

func addMember(boards BoardService) func(context.Context, *mcp.CallToolRequest, addMemberInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in addMemberInput) (*mcp.CallToolResult, any, error) {
		m, err := boards.AddMember(ctx, service.AddMember{Board: in.Board, Person: in.Person, Role: in.Role, CanComment: in.CanComment})
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(toMemberOut(m))
	}
}

func updateMember(boards BoardService) func(context.Context, *mcp.CallToolRequest, updateMemberInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in updateMemberInput) (*mcp.CallToolResult, any, error) {
		m, err := boards.UpdateMember(ctx, service.UpdateMember{Board: in.Board, Person: in.Person, Role: in.Role, CanComment: in.CanComment})
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(toMemberOut(m))
	}
}

func removeMember(boards BoardService) func(context.Context, *mcp.CallToolRequest, removeMemberInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in removeMemberInput) (*mcp.CallToolResult, any, error) {
		m, err := boards.RemoveMember(ctx, in.Board, in.Person)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(removedMemberOut{Removed: m.Name, Role: m.Role})
	}
}

func toMemberOut(m *service.Member) memberOut {
	return memberOut{Name: m.Name, Username: m.Username, Role: m.Role, CanComment: m.CanComment}
}
