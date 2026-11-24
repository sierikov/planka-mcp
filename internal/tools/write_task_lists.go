package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sierikov/planka-mcp/internal/service"
)

type createChecklistInput struct {
	Card               string `json:"card" jsonschema:"card ID, card URL, or title (or part of it); a match in the description alone is refused"`
	Board              string `json:"board,omitempty" jsonschema:"board to look on when card is a title (ID, URL or name)"`
	Name               string `json:"name" jsonschema:"the new checklist's name; it must differ from the card's other checklists"`
	After              string `json:"after,omitempty" jsonschema:"put it right after this checklist (exact name or ID); without after or before it goes last"`
	Before             string `json:"before,omitempty" jsonschema:"put it right before this checklist (exact name or ID)"`
	ShowOnFrontOfCard  *bool  `json:"show_on_front_of_card,omitempty" jsonschema:"show the checklist on the card in the board view (default true)"`
	HideCompletedTasks *bool  `json:"hide_completed_tasks,omitempty" jsonschema:"hide its ticked tasks (default false)"`
}

type updateChecklistInput struct {
	Card               string  `json:"card" jsonschema:"card ID, card URL, or title (or part of it); a match in the description alone is refused"`
	Board              string  `json:"board,omitempty" jsonschema:"board to look on when card is a title (ID, URL or name)"`
	Checklist          string  `json:"checklist" jsonschema:"the checklist to change, by exact name or ID"`
	Name               *string `json:"name,omitempty" jsonschema:"new name"`
	After              string  `json:"after,omitempty" jsonschema:"move it right after this checklist (exact name or ID)"`
	Before             string  `json:"before,omitempty" jsonschema:"move it right before this checklist (exact name or ID)"`
	ShowOnFrontOfCard  *bool   `json:"show_on_front_of_card,omitempty" jsonschema:"show the checklist on the card in the board view"`
	HideCompletedTasks *bool   `json:"hide_completed_tasks,omitempty" jsonschema:"hide its ticked tasks"`
}

type deleteChecklistInput struct {
	Card      string `json:"card" jsonschema:"card ID, card URL, or title (or part of it); a match in the description alone is refused"`
	Board     string `json:"board,omitempty" jsonschema:"board to look on when card is a title (ID, URL or name)"`
	Checklist string `json:"checklist" jsonschema:"the checklist to delete, by exact name or ID"`
	WithTasks bool   `json:"with_tasks,omitempty" jsonschema:"also delete the tasks still in it (default false: a checklist with tasks is refused)"`
}

// checklistOut is a checklist as a checklist write leaves it.
type checklistOut struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	ShowOnFrontOfCard  bool   `json:"show_on_front_of_card"`
	HideCompletedTasks bool   `json:"hide_completed_tasks"`
	TasksTotal         int    `json:"tasks_total"`
}

// checklistResultOut is a checklist after a write, with the names of the
// card's checklists in order.
type checklistResultOut struct {
	Checklist      checklistOut `json:"checklist"`
	ChecklistOrder []string     `json:"checklist_order"`
}

// deletedChecklistOut confirms a deleted checklist and lists the tasks
// deleted with it, so they can be added again.
type deletedChecklistOut struct {
	Deleted string    `json:"deleted"`
	Name    string    `json:"name"`
	Tasks   []taskOut `json:"tasks,omitempty"`
}

// registerChecklistTools adds the tools that create, change and delete the
// checklists of a card.
func registerChecklistTools(server *mcp.Server, tasks TaskService) {
	addTool(server, &mcp.Tool{
		Name:  "create_checklist",
		Title: "Create checklist",
		Description: "Use this to add an empty checklist to a Planka card, placed after or before another checklist, or last. " +
			"To add tasks, use add_tasks, which also creates a checklist it does not find. " +
			"Returns the checklist (id, name, display options, tasks_total) and checklist_order, the card's checklist names in order.",
		Annotations: additive,
	}, createChecklist(tasks))

	addTool(server, &mcp.Tool{
		Name:  "update_checklist",
		Title: "Update checklist",
		Description: "Use this to rename or reorder a checklist on a Planka card, or to change whether it shows on the card and hides ticked tasks. " +
			"Fields you leave out stay as they are. To move tasks between checklists, use update_tasks. " +
			"Returns the checklist and checklist_order.",
		Annotations: idempotent,
	}, updateChecklist(tasks))

	addTool(server, &mcp.Tool{
		Name:  "delete_checklist",
		Title: "Delete checklist",
		Description: "Use this only when a checklist on a Planka card must go. Planka deletes the tasks in it for good, " +
			"so a checklist with tasks is refused unless with_tasks=true; to keep them, move them first with update_tasks. " +
			"Returns the deleted checklist's id and name, and the tasks deleted with it.",
		Annotations: destructive,
	}, deleteChecklist(tasks))
}

func createChecklist(tasks TaskService) func(context.Context, *mcp.CallToolRequest, createChecklistInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in createChecklistInput) (*mcp.CallToolResult, any, error) {
		res, err := tasks.CreateChecklist(ctx, service.CreateChecklist{
			Card: service.CardRef{Ref: in.Card, Board: in.Board}, Name: in.Name,
			Place:             service.ListPlace{After: in.After, Before: in.Before},
			ShowOnFrontOfCard: in.ShowOnFrontOfCard, HideCompletedTasks: in.HideCompletedTasks,
		})
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(toChecklistResultOut(res))
	}
}

func updateChecklist(tasks TaskService) func(context.Context, *mcp.CallToolRequest, updateChecklistInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in updateChecklistInput) (*mcp.CallToolResult, any, error) {
		res, err := tasks.UpdateChecklist(ctx, service.UpdateChecklist{
			Card: service.CardRef{Ref: in.Card, Board: in.Board}, Checklist: in.Checklist, Name: in.Name,
			Place:             service.ListPlace{After: in.After, Before: in.Before},
			ShowOnFrontOfCard: in.ShowOnFrontOfCard, HideCompletedTasks: in.HideCompletedTasks,
		})
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(toChecklistResultOut(res))
	}
}

func deleteChecklist(tasks TaskService) func(context.Context, *mcp.CallToolRequest, deleteChecklistInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in deleteChecklistInput) (*mcp.CallToolResult, any, error) {
		res, err := tasks.DeleteChecklist(ctx, service.CardRef{Ref: in.Card, Board: in.Board}, in.Checklist, in.WithTasks)
		if err != nil {
			return errorResult(err)
		}
		out := deletedChecklistOut{Deleted: res.ID, Name: res.Name}
		for _, t := range res.Tasks {
			out.Tasks = append(out.Tasks, toTaskOut(t))
		}
		return jsonResult(out)
	}
}

func toChecklistResultOut(r *service.ChecklistResult) checklistResultOut {
	c := r.Checklist
	return checklistResultOut{
		Checklist: checklistOut{
			ID: c.ID, Name: c.Name, ShowOnFrontOfCard: c.ShowOnFrontOfCard, HideCompletedTasks: c.HideCompletedTasks, TasksTotal: len(c.Tasks),
		},
		ChecklistOrder: r.Order,
	}
}
