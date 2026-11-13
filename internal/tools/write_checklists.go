package tools

import (
	"context"
	"errors"

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
}

type updateTasksInput struct {
	Items []taskChangeInput `json:"items" jsonschema:"tasks to change; each has a task_id and a completed or name"`
}

type deleteTaskInput struct {
	TaskID string `json:"task_id" jsonschema:"ID of the task to delete, from get_card"`
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
		Description: "Use this to tick, untick or rename tasks on Planka cards, several in one call. " +
			"A task that links a card cannot be changed: Planka ticks it when that card moves to a closed list such as Done. " +
			"Returns the changed tasks.",
		Annotations: idempotent,
	}, updateTasks(tasks))

	addTool(server, &mcp.Tool{
		Name:        "delete_task",
		Title:       "Delete task",
		Description: "Use this to remove a task from a checklist on a Planka card for good. Returns the deleted task's id.",
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
		return jsonResult(toTaskListOut(*list))
	}
}

func updateTasks(tasks TaskService) func(context.Context, *mcp.CallToolRequest, updateTasksInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in updateTasksInput) (*mcp.CallToolResult, any, error) {
		updates := make([]service.TaskUpdate, len(in.Items))
		for i, it := range in.Items {
			updates[i] = service.TaskUpdate{ID: it.TaskID, Completed: it.Completed, Name: it.Name}
		}
		changed, err := tasks.Update(ctx, updates)
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
		if err := tasks.Delete(ctx, in.TaskID); err != nil {
			return errorResult(err)
		}
		return jsonResult(deletedOut{Deleted: in.TaskID})
	}
}
