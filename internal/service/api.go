package service

import (
	"context"

	"github.com/sierikov/planka-mcp/internal/planka"
)

// The interfaces below are the slices of *planka.Client the services use.

// ProjectsAPI lists the projects, with their boards.
type ProjectsAPI interface {
	Projects(ctx context.Context) (*planka.ProjectsResponse, error)
}

// BoardsAPI reads a board with its lists, cards and labels.
type BoardsAPI interface {
	Board(ctx context.Context, id string) (*planka.BoardResponse, error)
}

// UsersAPI identifies the user the API key belongs to.
type UsersAPI interface {
	Me(ctx context.Context) (*planka.User, error)
}

// CardsReadAPI reads cards.
type CardsReadAPI interface {
	Card(ctx context.Context, id string) (*planka.CardResponse, error)
	ListCards(ctx context.Context, listID string, q planka.CardQuery) (*planka.CardsResponse, error)
}

// CommentsReadAPI reads a card's comments.
type CommentsReadAPI interface {
	Comments(ctx context.Context, cardID, beforeID string) (*planka.CommentsResponse, error)
}

// CardsWriteAPI creates, changes and deletes cards.
type CardsWriteAPI interface {
	CreateCard(ctx context.Context, listID string, r planka.CreateCardRequest) (*planka.Card, error)
	UpdateCard(ctx context.Context, id string, r planka.UpdateCardRequest) (*planka.Card, error)
	DeleteCard(ctx context.Context, id string) error
}

// LabelsAPI attaches labels to cards and creates board labels.
type LabelsAPI interface {
	AddCardLabel(ctx context.Context, cardID, labelID string) error
	RemoveCardLabel(ctx context.Context, cardID, labelID string) error
	CreateLabel(ctx context.Context, boardID, name, color string, position float64) (*planka.Label, error)
}

// MembersAPI assigns users to cards.
type MembersAPI interface {
	AddCardMember(ctx context.Context, cardID, userID string) error
	RemoveCardMember(ctx context.Context, cardID, userID string) error
}

// CommentsWriteAPI adds, changes and deletes comments.
type CommentsWriteAPI interface {
	CreateComment(ctx context.Context, cardID, text string) (*planka.Comment, error)
	UpdateComment(ctx context.Context, id, text string) (*planka.Comment, error)
	DeleteComment(ctx context.Context, id string) error
}

// TasksAPI creates task lists, and creates, changes and deletes tasks.
type TasksAPI interface {
	CreateTaskList(ctx context.Context, cardID, name string, position float64) (*planka.TaskList, error)
	CreateTask(ctx context.Context, taskListID string, r planka.CreateTaskRequest) (*planka.Task, error)
	UpdateTask(ctx context.Context, id string, r planka.UpdateTaskRequest) (*planka.Task, error)
	DeleteTask(ctx context.Context, id string) error
}
