package tools

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sierikov/planka-mcp/internal/service"
)

type addCommentInput struct {
	Card  string `json:"card" jsonschema:"card ID, card URL, or title (or part of it); a match in the description alone is refused"`
	Board string `json:"board,omitempty" jsonschema:"board to look on when card is a title (ID, URL or name)"`
	Text  string `json:"text" jsonschema:"comment text (markdown)"`
}

type updateCommentInput struct {
	CommentID string `json:"comment_id" jsonschema:"ID of the comment to edit"`
	Text      string `json:"text" jsonschema:"new comment text (markdown); it replaces the old text"`
}

type deleteCommentInput struct {
	CommentID string `json:"comment_id" jsonschema:"ID of the comment to delete"`
}

type newTaskInput struct {
	Name string `json:"name,omitempty" jsonschema:"task text; give either name or link"`
	Link string `json:"link,omitempty" jsonschema:"card ID, URL or title to link as the task, instead of a name; a match in the description alone is refused"`
}

type addTasksInput struct {
	Card     string         `json:"card" jsonschema:"card ID, card URL, or title (or part of it); a match in the description alone is refused"`
	Board    string         `json:"board,omitempty" jsonschema:"board to look on when card is a title (ID, URL or name)"`
	TaskList string         `json:"task_list,omitempty" jsonschema:"checklist name (default Tasks); created when the card lacks it"`
	Items    []newTaskInput `json:"items" jsonschema:"tasks to append, in order; each has a name or a link"`
}

type taskChangeInput struct {
	TaskID    string  `json:"task_id" jsonschema:"ID of the task, from get_card"`
	Completed *bool   `json:"completed,omitempty" jsonschema:"tick (true) or untick (false) the task"`
	Name      *string `json:"name,omitempty" jsonschema:"new task text"`
	Checklist string  `json:"checklist,omitempty" jsonschema:"move the task to this checklist of the same card (exact name or ID); it keeps its id and done state, and goes to the bottom unless position, after or before says otherwise"`
	Position  string  `json:"position,omitempty" jsonschema:"top or bottom of its checklist"`
	After     string  `json:"after,omitempty" jsonschema:"put the task right after this task (ID) of its checklist"`
	Before    string  `json:"before,omitempty" jsonschema:"put the task right before this task (ID) of its checklist"`
	Assignee  *string `json:"assignee,omitempty" jsonschema:"assign the task to a board member by name, or me; an empty string removes the assignee"`
}

type updateTasksInput struct {
	Card  string            `json:"card,omitempty" jsonschema:"the card the tasks are on (ID, URL or title); needed to move, reorder or assign tasks"`
	Board string            `json:"board,omitempty" jsonschema:"board to look on when card is a title (ID, URL or name)"`
	Items []taskChangeInput `json:"items" jsonschema:"tasks to change, applied in order; each has a task_id and at least one change"`
}

type deleteTaskInput struct {
	TaskID  string   `json:"task_id,omitempty" jsonschema:"ID of the task to delete, from get_card"`
	TaskIDs []string `json:"task_ids,omitempty" jsonschema:"IDs of several tasks to delete in one call, instead of task_id"`
}

// registerChecklistWriteTools adds the tools that change comments and checklist tasks.
func registerChecklistWriteTools(server *mcp.Server, comments CommentService, tasks TaskService) {
	addTool(server, &mcp.Tool{
		Name:  "add_comment",
		Title: "Add comment",
		Description: "Use this to comment on a Planka card, for example a summary of the work done on it. " +
			"Returns the comment (id, author, date, text).",
		Annotations: additive,
	}, addComment(comments))

	addTool(server, &mcp.Tool{
		Name:  "update_comment",
		Title: "Edit comment",
		Description: "Use this to edit the text of a comment on a Planka card; it replaces the old text. " +
			"Returns the comment (id, date, text) without its author.",
		Annotations: idempotent,
	}, updateComment(comments))

	addTool(server, &mcp.Tool{
		Name:        "delete_comment",
		Title:       "Delete comment",
		Description: "Use this to remove a comment from a Planka card for good. Returns the deleted comment's id.",
		Annotations: destructive,
	}, deleteComment(comments))

	addTool(server, &mcp.Tool{
		Name:  "add_tasks",
		Title: "Add tasks",
		Description: "Use this to append tasks to a checklist on a Planka card, either plain names or links to other cards, many in one call. " +
			"Returns the checklist with all its tasks and their ids.",
		Annotations: additive,
	}, addTasks(tasks))

	addTool(server, &mcp.Tool{
		Name:  "update_tasks",
		Title: "Update tasks",
		Description: "Use this to tick, untick, rename, reorder or assign tasks on a Planka card, or move them to another checklist of the same card, " +
			"several in one call (give card to move, reorder or assign). " +
			"A task that links a card can only be moved or reordered: Planka ticks it when that card moves to a closed list such as Done. " +
			"Returns the changed tasks, with their checklist when card is given.",
		Annotations: idempotent,
		InputSchema: taskChangeSchema(),
	}, updateTasks(tasks))

	addTool(server, &mcp.Tool{
		Name:  "delete_task",
		Title: "Delete task",
		Description: "Use this to remove tasks from the checklists of a Planka card for good: one with task_id, or several with task_ids. " +
			"Returns the deleted tasks' ids.",
		Annotations: destructive,
	}, deleteTask(tasks))
}

func addComment(comments CommentService) func(context.Context, *mcp.CallToolRequest, addCommentInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in addCommentInput) (*mcp.CallToolResult, any, error) {
		comment, err := comments.Add(ctx, service.CardRef{Ref: in.Card, Board: in.Board}, in.Text)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(toCommentOut(*comment))
	}
}

func updateComment(comments CommentService) func(context.Context, *mcp.CallToolRequest, updateCommentInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in updateCommentInput) (*mcp.CallToolResult, any, error) {
		comment, err := comments.Update(ctx, in.CommentID, in.Text)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(toCommentOut(*comment))
	}
}

func deleteComment(comments CommentService) func(context.Context, *mcp.CallToolRequest, deleteCommentInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in deleteCommentInput) (*mcp.CallToolResult, any, error) {
		if err := comments.Delete(ctx, in.CommentID); err != nil {
			return errorResult(err)
		}
		return jsonResult(deletedOut{Deleted: in.CommentID})
	}
}

func addTasks(tasks TaskService) func(context.Context, *mcp.CallToolRequest, addTasksInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in addTasksInput) (*mcp.CallToolResult, any, error) {
		if len(in.Items) == 0 {
			return errorResult(errors.New("items is empty: give at least one task as {name} or {link}"))
		}
		items := make([]service.TaskInput, len(in.Items))
		for i, it := range in.Items {
			items[i] = service.TaskInput{Name: it.Name, Link: it.Link}
		}
		list, err := tasks.Add(ctx, service.CardRef{Ref: in.Card, Board: in.Board}, in.TaskList, items)
		if err != nil {
			return errorResult(err)
		}
		out := addTasksOut{taskListOut: toTaskListOut(*list), Created: list.Created}
		if list.Created && len(list.Others) > 0 {
			out.Hint = fmt.Sprintf("this call created the checklist %q; the card already had %s. "+
				"If you meant one of those, move these tasks there with update_tasks (give card and checklist), "+
				"then delete the new checklist with delete_checklist",
				list.Name, quoteList(list.Others))
		}
		return jsonResult(out)
	}
}

// addTasksOut is the checklist add_tasks appended to. Created says the call
// made it; Hint then names the card's other checklists, in case the name was a
// typo of one of them.
type addTasksOut struct {
	taskListOut
	Created bool   `json:"created,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

// quoteList quotes each name and joins them: "A", "B".
func quoteList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = strconv.Quote(n)
	}
	return strings.Join(quoted, ", ")
}

func updateTasks(tasks TaskService) func(context.Context, *mcp.CallToolRequest, updateTasksInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in updateTasksInput) (*mcp.CallToolResult, any, error) {
		updates := make([]service.TaskUpdate, len(in.Items))
		for i, it := range in.Items {
			updates[i] = service.TaskUpdate{
				ID: it.TaskID, Completed: it.Completed, Name: it.Name,
				Checklist: it.Checklist, Position: it.Position, After: it.After, Before: it.Before, Assignee: it.Assignee,
			}
		}
		changed, err := tasks.Update(ctx, service.CardRef{Ref: in.Card, Board: in.Board}, updates)
		if err != nil {
			return errorResult(err)
		}
		out := make([]taskOut, len(changed))
		for i, t := range changed {
			out[i] = toTaskOut(t)
		}
		return jsonResult(out)
	}
}

func deleteTask(tasks TaskService) func(context.Context, *mcp.CallToolRequest, deleteTaskInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in deleteTaskInput) (*mcp.CallToolResult, any, error) {
		switch {
		case in.TaskID != "" && len(in.TaskIDs) > 0:
			return errorResult(errors.New("give task_id or task_ids, not both"))
		case in.TaskID != "":
			if err := tasks.Delete(ctx, []string{in.TaskID}); err != nil {
				return errorResult(err)
			}
			return jsonResult(deletedOut{Deleted: in.TaskID})
		case len(in.TaskIDs) == 0:
			return errorResult(errors.New("give task_id, or task_ids for several tasks"))
		}
		if err := tasks.Delete(ctx, in.TaskIDs); err != nil {
			return errorResult(err)
		}
		return jsonResult(deletedTasksOut{Deleted: in.TaskIDs})
	}
}

// deletedTasksOut confirms the tasks delete_task removed with task_ids.
type deletedTasksOut struct {
	Deleted []string `json:"deleted"`
}

// taskChangeSchema is update_tasks' input schema, with position limited to
// top and bottom inside each item.
func taskChangeSchema() *jsonschema.Schema {
	s := inputSchema[updateTasksInput](nil)
	item := s.Properties["items"].Items
	for _, p := range service.Positions {
		item.Properties["position"].Enum = append(item.Properties["position"].Enum, p)
	}
	return s
}
