package service

import (
	"context"

	"github.com/sierikov/planka-mcp/internal/planka"
	"github.com/sierikov/planka-mcp/internal/unsplash"
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

// ListsAPI creates, changes, sorts, empties and deletes a board's lists.
type ListsAPI interface {
	CreateList(ctx context.Context, boardID string, r planka.CreateListRequest) (*planka.List, error)
	UpdateList(ctx context.Context, id string, r planka.UpdateListRequest) (*planka.List, error)
	DeleteList(ctx context.Context, id string) ([]planka.Card, error)
	SortList(ctx context.Context, id, field, order string) error
	MoveListCards(ctx context.Context, id, toListID string) ([]planka.Card, error)
}

// TasksAPI creates, changes and deletes task lists (checklists) and tasks.
type TasksAPI interface {
	CreateTaskList(ctx context.Context, cardID string, r planka.CreateTaskListRequest) (*planka.TaskList, error)
	UpdateTaskList(ctx context.Context, id string, r planka.UpdateTaskListRequest) (*planka.TaskList, error)
	DeleteTaskList(ctx context.Context, id string) error
	CreateTask(ctx context.Context, taskListID string, r planka.CreateTaskRequest) (*planka.Task, error)
	UpdateTask(ctx context.Context, id string, r planka.UpdateTaskRequest) (*planka.Task, error)
	DeleteTask(ctx context.Context, id string) error
}

// AttachmentsAPI adds, renames and deletes the files and links on cards.
type AttachmentsAPI interface {
	CreateFileAttachment(ctx context.Context, cardID string, f planka.FileUpload) (*planka.Attachment, error)
	CreateLinkAttachment(ctx context.Context, cardID, name, url string) (*planka.Attachment, error)
	UpdateAttachment(ctx context.Context, id, name string) (*planka.Attachment, error)
	DeleteAttachment(ctx context.Context, id string) error
}

// BoardLabelsAPI creates, changes and deletes board labels.
type BoardLabelsAPI interface {
	CreateLabel(ctx context.Context, boardID, name, color string, position float64) (*planka.Label, error)
	UpdateLabel(ctx context.Context, id string, r planka.UpdateLabelRequest) (*planka.Label, error)
	DeleteLabel(ctx context.Context, id string) error
}

// CustomFieldsAPI sets and clears a card's custom field values.
type CustomFieldsAPI interface {
	SetCustomFieldValue(ctx context.Context, cardID, groupID, fieldID, content string) (*planka.CustomFieldValue, error)
	ClearCustomFieldValue(ctx context.Context, cardID, groupID, fieldID string) error
}

// ProjectWriteAPI creates, changes and deletes projects, manages their
// managers, and lists users.
type ProjectWriteAPI interface {
	CreateProject(ctx context.Context, r planka.CreateProjectRequest) (*planka.Project, error)
	UpdateProject(ctx context.Context, id string, r planka.UpdateProjectRequest) (*planka.Project, error)
	DeleteProject(ctx context.Context, id string) error
	CreateProjectManager(ctx context.Context, projectID, userID string) (*planka.ProjectManager, error)
	DeleteProjectManager(ctx context.Context, id string) error
	CreateBackgroundImage(ctx context.Context, projectID string, f planka.FileUpload) (*planka.BackgroundImage, error)
	Users(ctx context.Context) ([]planka.User, error)
}

// PhotosAPI fetches photos from Unsplash for project backgrounds. Search, and
// Link to a photo page, work only when CanSearch says so.
type PhotosAPI interface {
	Link(ctx context.Context, link string) (*unsplash.Photo, error)
	Search(ctx context.Context, words string) (*unsplash.Photo, error)
	CanSearch() bool
}

// BoardWriteAPI creates, renames and deletes boards, manages their members,
// and lists users.
type BoardWriteAPI interface {
	CreateBoard(ctx context.Context, projectID, name string, position float64) (*planka.Board, error)
	UpdateBoard(ctx context.Context, id, name string) (*planka.Board, error)
	DeleteBoard(ctx context.Context, id string) error
	CreateBoardMembership(ctx context.Context, boardID string, r planka.BoardMembershipRequest) (*planka.BoardMembership, error)
	UpdateBoardMembership(ctx context.Context, id string, r planka.BoardMembershipRequest) (*planka.BoardMembership, error)
	DeleteBoardMembership(ctx context.Context, id string) error
	Users(ctx context.Context) ([]planka.User, error)
}
