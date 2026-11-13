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

// TaskService adds, changes and deletes checklist tasks.
type TaskService interface {
	Add(ctx context.Context, ref service.CardRef, taskList string, items []service.TaskInput) (*service.TaskList, error)
	Update(ctx context.Context, items []service.TaskUpdate) ([]service.Task, error)
	Delete(ctx context.Context, id string) error
}

// Deps are the services the tools call. Every service is required.
type Deps struct {
	Catalog  CatalogService
	Cards    CardService
	Comments CommentService
	Tasks    TaskService
}
