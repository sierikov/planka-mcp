package planka

import (
	"encoding/json"
	"time"
)

// The wire types mirror Planka's JSON. IDs are strings, times are RFC 3339,
// and a nullable field is a pointer. Only the fields the server uses are kept.

// User is a Planka account.
type User struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

// Project groups boards.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Board is a kanban board.
type Board struct {
	ID              string  `json:"id"`
	ProjectID       string  `json:"projectId"`
	Name            string  `json:"name"`
	Position        float64 `json:"position"`
	DefaultCardType string  `json:"defaultCardType"`
}

// List is a column of a board. Type is active, closed, archive or trash; the
// archive and trash lists have no name and no position.
type List struct {
	ID       string   `json:"id"`
	BoardID  string   `json:"boardId"`
	Type     string   `json:"type"`
	Name     *string  `json:"name"`
	Position *float64 `json:"position"`
}

// Card is a card on a board. Position is null for cards in the archive list.
type Card struct {
	ID             string     `json:"id"`
	BoardID        string     `json:"boardId"`
	ListID         string     `json:"listId"`
	Type           string     `json:"type"`
	Name           string     `json:"name"`
	Description    *string    `json:"description"`
	DueDate        *time.Time `json:"dueDate"`
	IsDueCompleted *bool      `json:"isDueCompleted"`
	Stopwatch      *Stopwatch `json:"stopwatch"`
	Position       *float64   `json:"position"`
	CommentsTotal  int        `json:"commentsTotal"`
	ListChangedAt  time.Time  `json:"listChangedAt"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      *time.Time `json:"updatedAt"`
}

// Stopwatch is a card's time tracker; Total is in seconds.
type Stopwatch struct {
	StartedAt *time.Time `json:"startedAt"`
	Total     int64      `json:"total"`
}

// MarshalJSON sends StartedAt in UTC with milliseconds, as Planka's own web
// client does (see wireTimeLayout), rather than with an offset and
// nanoseconds, which Planka would store verbatim.
func (s Stopwatch) MarshalJSON() ([]byte, error) {
	out := struct {
		StartedAt *string `json:"startedAt"`
		Total     int64   `json:"total"`
	}{Total: s.Total}
	if s.StartedAt != nil {
		started := wireTime(*s.StartedAt)
		out.StartedAt = &started
	}
	return json.Marshal(out)
}

// Label is a board label. Its name can be null.
type Label struct {
	ID       string  `json:"id"`
	BoardID  string  `json:"boardId"`
	Name     *string `json:"name"`
	Color    string  `json:"color"`
	Position float64 `json:"position"`
}

// CardLabel attaches a label to a card.
type CardLabel struct {
	ID      string `json:"id"`
	CardID  string `json:"cardId"`
	LabelID string `json:"labelId"`
}

// CardMembership assigns a user to a card.
type CardMembership struct {
	ID     string `json:"id"`
	CardID string `json:"cardId"`
	UserID string `json:"userId"`
}

// BoardMembership gives a user access to a board.
type BoardMembership struct {
	ID      string `json:"id"`
	BoardID string `json:"boardId"`
	UserID  string `json:"userId"`
	Role    string `json:"role"`
}

// TaskList is a checklist on a card.
type TaskList struct {
	ID       string  `json:"id"`
	CardID   string  `json:"cardId"`
	Name     string  `json:"name"`
	Position float64 `json:"position"`
}

// Task is a checklist item. A linked task has LinkedCardID set, and Planka
// fills Name with the linked card's name.
type Task struct {
	ID           string  `json:"id"`
	TaskListID   string  `json:"taskListId"`
	LinkedCardID *string `json:"linkedCardId"`
	Name         *string `json:"name"`
	IsCompleted  bool    `json:"isCompleted"`
	Position     float64 `json:"position"`
}

// Attachment is a file or link attached to a card.
type Attachment struct {
	ID     string         `json:"id"`
	CardID string         `json:"cardId"`
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Data   map[string]any `json:"data"`
}

// CustomFieldGroup groups custom fields, on a board or on a card. A group made
// from a project's base group has no name of its own: its name and its fields
// are the base group's.
type CustomFieldGroup struct {
	ID                     string  `json:"id"`
	BoardID                *string `json:"boardId"`
	CardID                 *string `json:"cardId"`
	BaseCustomFieldGroupID *string `json:"baseCustomFieldGroupId"`
	Name                   *string `json:"name"`
	Position               float64 `json:"position"`
}

// BaseCustomFieldGroup is a project's reusable group of custom fields.
type BaseCustomFieldGroup struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
}

// CustomField is one field of a group, or of a base group.
type CustomField struct {
	ID                     string  `json:"id"`
	CustomFieldGroupID     string  `json:"customFieldGroupId"`
	BaseCustomFieldGroupID *string `json:"baseCustomFieldGroupId"`
	Name                   string  `json:"name"`
	Position               float64 `json:"position"`
	ShowOnFrontOfCard      bool    `json:"showOnFrontOfCard"`
}

// CustomFieldValue is a card's value for a custom field.
type CustomFieldValue struct {
	ID                 string `json:"id"`
	CardID             string `json:"cardId"`
	CustomFieldGroupID string `json:"customFieldGroupId"`
	CustomFieldID      string `json:"customFieldId"`
	Content            string `json:"content"`
}

// Comment is a comment on a card.
type Comment struct {
	ID        string    `json:"id"`
	CardID    string    `json:"cardId"`
	UserID    string    `json:"userId"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}

// ProjectsResponse is the answer to GET /api/projects.
type ProjectsResponse struct {
	Items    []Project        `json:"items"`
	Included ProjectsIncluded `json:"included"`
}

// ProjectsIncluded holds the records sideloaded with the projects. CustomFields
// are the fields of the base groups.
type ProjectsIncluded struct {
	Boards                []Board                `json:"boards"`
	BaseCustomFieldGroups []BaseCustomFieldGroup `json:"baseCustomFieldGroups"`
	CustomFields          []CustomField          `json:"customFields"`
}

// BoardIncluded holds the records sideloaded with a board. Archived and trash
// cards are not among Cards; read them through ListCards.
type BoardIncluded struct {
	Lists             []List             `json:"lists"`
	Cards             []Card             `json:"cards"`
	Labels            []Label            `json:"labels"`
	CardLabels        []CardLabel        `json:"cardLabels"`
	CardMemberships   []CardMembership   `json:"cardMemberships"`
	BoardMemberships  []BoardMembership  `json:"boardMemberships"`
	Users             []User             `json:"users"`
	TaskLists         []TaskList         `json:"taskLists"`
	Tasks             []Task             `json:"tasks"`
	Attachments       []Attachment       `json:"attachments"`
	CustomFieldGroups []CustomFieldGroup `json:"customFieldGroups"`
	CustomFields      []CustomField      `json:"customFields"`
	CustomFieldValues []CustomFieldValue `json:"customFieldValues"`
	Projects          []Project          `json:"projects"`
}

// BoardResponse is the answer to GET /api/boards/:id.
type BoardResponse struct {
	Item     Board         `json:"item"`
	Included BoardIncluded `json:"included"`
}

// CardIncluded holds the records sideloaded with a card.
type CardIncluded struct {
	CardLabels        []CardLabel        `json:"cardLabels"`
	CardMemberships   []CardMembership   `json:"cardMemberships"`
	TaskLists         []TaskList         `json:"taskLists"`
	Tasks             []Task             `json:"tasks"`
	Attachments       []Attachment       `json:"attachments"`
	CustomFieldGroups []CustomFieldGroup `json:"customFieldGroups"`
	CustomFields      []CustomField      `json:"customFields"`
	CustomFieldValues []CustomFieldValue `json:"customFieldValues"`
	Users             []User             `json:"users"`
}

// CardResponse is the answer to GET /api/cards/:id.
type CardResponse struct {
	Item     Card         `json:"item"`
	Included CardIncluded `json:"included"`
}

// CardsResponse is the answer to GET /api/lists/:id/cards.
type CardsResponse struct {
	Items    []Card `json:"items"`
	Included struct {
		Users           []User           `json:"users"`
		CardLabels      []CardLabel      `json:"cardLabels"`
		CardMemberships []CardMembership `json:"cardMemberships"`
	} `json:"included"`
}

// CommentsResponse is the answer to GET /api/cards/:id/comments, newest first.
type CommentsResponse struct {
	Items    []Comment `json:"items"`
	Included struct {
		Users []User `json:"users"`
	} `json:"included"`
}

// CardQuery filters and pages ListCards.
type CardQuery struct {
	// Search is a case-insensitive text filter (Planka caps it at 128 characters).
	Search string
	// Before is the cursor of the last card of the previous page.
	Before *CardCursor
}

// CardCursor identifies the card after which the next page of ListCards starts.
type CardCursor struct {
	ListChangedAt time.Time
	ID            string
}
