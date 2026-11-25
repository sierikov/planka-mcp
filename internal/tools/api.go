// Package tools is the MCP wiring of the Planka server: typed input structs,
// one service call per tool, compact output shaping and error text. It talks
// to the services through the interfaces declared here.
package tools

import (
	"context"

	"github.com/sierikov/planka-mcp/internal/service"
)

// CatalogService finds boards and reads one as a kanban.
type CatalogService interface {
	ListBoards(ctx context.Context, query string) ([]service.ProjectBoards, error)
	GetBoard(ctx context.Context, ref, list string) (*service.Board, error)
}

// CardService reads and writes cards.
type CardService interface {
	Search(ctx context.Context, q service.SearchQuery) ([]service.CardSummary, int, error)
	Get(ctx context.Context, ref service.CardRef, commentsLimit int) (*service.Card, error)
	Comments(ctx context.Context, ref service.CardRef, before string, limit int) ([]service.Comment, bool, error)
	Create(ctx context.Context, in service.CreateCard) (*service.WriteResult, error)
	Move(ctx context.Context, in service.MoveCard) (*service.WriteResult, error)
	Update(ctx context.Context, in service.UpdateCard) (*service.WriteResult, error)
	Delete(ctx context.Context, ref service.CardRef) (*service.CardSummary, error)
}

// CommentService adds, changes and deletes comments.
type CommentService interface {
	Add(ctx context.Context, ref service.CardRef, text string) (*service.Comment, error)
	Update(ctx context.Context, id, text string) (*service.Comment, error)
	Delete(ctx context.Context, id string) error
}

// TaskService adds, changes and deletes checklists and their tasks.
type TaskService interface {
	Add(ctx context.Context, ref service.CardRef, taskList string, items []service.TaskInput) (*service.TaskList, error)
	Update(ctx context.Context, card service.CardRef, items []service.TaskUpdate) ([]service.Task, error)
	Delete(ctx context.Context, ids []string) error
	CreateChecklist(ctx context.Context, in service.CreateChecklist) (*service.ChecklistResult, error)
	UpdateChecklist(ctx context.Context, in service.UpdateChecklist) (*service.ChecklistResult, error)
	DeleteChecklist(ctx context.Context, card service.CardRef, checklist string, withTasks bool) (*service.DeletedChecklist, error)
}

// ListService adds, changes and deletes the lists (columns) of a board.
type ListService interface {
	Create(ctx context.Context, in service.CreateList) (*service.ListResult, error)
	Update(ctx context.Context, in service.UpdateList) (*service.ListResult, error)
	Delete(ctx context.Context, board, list string) (*service.DeletedList, error)
}

// AttachmentService adds, changes and deletes the files and links on cards.
type AttachmentService interface {
	Add(ctx context.Context, in service.AddAttachment) (*service.Attachment, error)
	Update(ctx context.Context, in service.UpdateAttachment) (*service.Attachment, error)
	Delete(ctx context.Context, card service.CardRef, attachment string) (*service.Attachment, error)
}

// Deps are the services the tools call. Every service is required.
type Deps struct {
	Catalog     CatalogService
	Cards       CardService
	Comments    CommentService
	Tasks       TaskService
	Lists       ListService
	Attachments AttachmentService

	// PublicURL is the base of the card links in results ("" leaves them out).
	PublicURL string
	// Uploads offers add_attachment's path: the server runs on the user's own
	// machine (stdio), so local files are the user's. Without it the tool
	// attaches links only.
	Uploads bool
}
