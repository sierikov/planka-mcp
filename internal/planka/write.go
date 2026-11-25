package planka

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Optional is a request field that can be left out, set to a value, or set to
// an explicit JSON null. The zero value is "left out". Planka needs the
// distinction: clearing a due date is "dueDate": null.
type Optional[T any] struct {
	// Set is true when the field is sent.
	Set bool
	// Null is true when the field is sent as JSON null; Value is then ignored.
	Null bool
	// Value is the field's value when Set and not Null.
	Value T
}

// Some returns an Optional that sends v.
func Some[T any](v T) Optional[T] {
	return Optional[T]{Set: true, Value: v}
}

// Null returns an Optional that sends an explicit JSON null.
func Null[T any]() Optional[T] {
	return Optional[T]{Set: true, Null: true}
}

// wireTimeLayout is how times are sent: UTC with millisecond precision, the
// form Planka's own web client sends. Planka 2.2.1 keeps only the wall clock
// of a due date and drops its offset, so "14:30+01:00" would be stored as
// 14:30 UTC, off by the whole offset.
const wireTimeLayout = "2006-01-02T15:04:05.000Z"

// wireTime formats t for a request body.
func wireTime(t time.Time) string {
	return t.UTC().Format(wireTimeLayout)
}

// CreateCardRequest is the body of POST /api/lists/:listId/cards.
type CreateCardRequest struct {
	// Type is "project" or "story"; Planka requires it.
	Type        string     `json:"type"`
	Position    float64    `json:"position"`
	Name        string     `json:"name"`
	Description *string    `json:"description,omitempty"`
	DueDate     *time.Time `json:"dueDate,omitempty"`
}

// MarshalJSON sends DueDate in UTC with milliseconds; see wireTimeLayout.
func (r CreateCardRequest) MarshalJSON() ([]byte, error) {
	type plain CreateCardRequest // drops this method, so Marshal does not recurse
	out := struct {
		plain
		DueDate *string `json:"dueDate,omitempty"` // shadows plain's
	}{plain: plain(r)}
	if r.DueDate != nil {
		due := wireTime(*r.DueDate)
		out.DueDate = &due
	}
	return json.Marshal(out)
}

// UpdateCardRequest is the body of PATCH /api/cards/:id. Only the fields that
// are Set are sent, and a Null field is sent as JSON null. Moving a card to
// another list needs Position as well as ListID. CoverAttachmentID must name a
// file attachment of the card; Null removes the cover.
type UpdateCardRequest struct {
	BoardID           Optional[string]
	ListID            Optional[string]
	Position          Optional[float64]
	Name              Optional[string]
	Description       Optional[string]
	DueDate           Optional[time.Time]
	IsDueCompleted    Optional[bool]
	Stopwatch         Optional[Stopwatch]
	CoverAttachmentID Optional[string]
}

// MarshalJSON emits only the Set fields, as null where Null is true. Times go
// out in UTC with milliseconds; see wireTimeLayout.
func (r UpdateCardRequest) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	put(m, "boardId", r.BoardID)
	put(m, "listId", r.ListID)
	put(m, "position", r.Position)
	put(m, "name", r.Name)
	put(m, "description", r.Description)
	due := r.DueDate
	put(m, "dueDate", Optional[string]{Set: due.Set, Null: due.Null, Value: wireTime(due.Value)})
	put(m, "isDueCompleted", r.IsDueCompleted)
	put(m, "stopwatch", r.Stopwatch)
	put(m, "coverAttachmentId", r.CoverAttachmentID)
	return json.Marshal(m)
}

// put adds o to m under key when it is set.
func put[T any](m map[string]any, key string, o Optional[T]) {
	switch {
	case !o.Set:
	case o.Null:
		m[key] = nil
	default:
		m[key] = o.Value
	}
}

// CreateTaskRequest is the body of POST /api/task-lists/:id/tasks. A task is
// either plain (Name) or linked to another card (LinkedCardID).
type CreateTaskRequest struct {
	Position     float64 `json:"position"`
	Name         *string `json:"name,omitempty"`
	LinkedCardID *string `json:"linkedCardId,omitempty"`
	IsCompleted  bool    `json:"isCompleted"`
}

// UpdateTaskRequest is the body of PATCH /api/tasks/:id; nil fields are left
// out. TaskListID moves the task to another checklist of the same card. A
// Null AssigneeUserID removes the assignee.
type UpdateTaskRequest struct {
	Name           *string
	IsCompleted    *bool
	TaskListID     *string
	Position       *float64
	AssigneeUserID Optional[string]
}

// MarshalJSON emits only the fields that are set.
func (r UpdateTaskRequest) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	if r.Name != nil {
		m["name"] = *r.Name
	}
	if r.IsCompleted != nil {
		m["isCompleted"] = *r.IsCompleted
	}
	if r.TaskListID != nil {
		m["taskListId"] = *r.TaskListID
	}
	if r.Position != nil {
		m["position"] = *r.Position
	}
	put(m, "assigneeUserId", r.AssigneeUserID)
	return json.Marshal(m)
}

// CreateTaskListRequest is the body of POST /api/cards/:cardId/task-lists. A
// nil display option takes Planka's default (shown on the card, completed
// tasks shown).
type CreateTaskListRequest struct {
	Position           float64 `json:"position"`
	Name               string  `json:"name"`
	ShowOnFrontOfCard  *bool   `json:"showOnFrontOfCard,omitempty"`
	HideCompletedTasks *bool   `json:"hideCompletedTasks,omitempty"`
}

// UpdateTaskListRequest is the body of PATCH /api/task-lists/:id. Only the
// fields that are Set are sent.
type UpdateTaskListRequest struct {
	Position           Optional[float64]
	Name               Optional[string]
	ShowOnFrontOfCard  Optional[bool]
	HideCompletedTasks Optional[bool]
}

// MarshalJSON emits only the Set fields.
func (r UpdateTaskListRequest) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	put(m, "position", r.Position)
	put(m, "name", r.Name)
	put(m, "showOnFrontOfCard", r.ShowOnFrontOfCard)
	put(m, "hideCompletedTasks", r.HideCompletedTasks)
	return json.Marshal(m)
}

// item is the {"item": ...} envelope of a single-record answer.
type item[T any] struct {
	Item T `json:"item"`
}

// send performs a write whose answer is a single record of type T.
func send[T any](ctx context.Context, c *Client, method, path string, body any) (*T, error) {
	var out item[T]
	if err := c.do(ctx, method, path, nil, body, &out); err != nil {
		return nil, err
	}
	return &out.Item, nil
}

// CreateCard creates a card in a list at the given position.
func (c *Client) CreateCard(ctx context.Context, listID string, r CreateCardRequest) (*Card, error) {
	return send[Card](ctx, c, http.MethodPost, "/api/lists/"+pathParam(listID)+"/cards", r)
}

// UpdateCard changes a card; only the Set fields of r are sent.
func (c *Client) UpdateCard(ctx context.Context, id string, r UpdateCardRequest) (*Card, error) {
	return send[Card](ctx, c, http.MethodPatch, "/api/cards/"+pathParam(id), r)
}

// DeleteCard permanently deletes a card.
func (c *Client) DeleteCard(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/cards/"+pathParam(id), nil, nil, nil)
}

// AddCardLabel attaches a label to a card.
func (c *Client) AddCardLabel(ctx context.Context, cardID, labelID string) error {
	body := map[string]string{"labelId": labelID}
	return c.do(ctx, http.MethodPost, "/api/cards/"+pathParam(cardID)+"/card-labels", nil, body, nil)
}

// RemoveCardLabel detaches a label from a card. Planka addresses the
// attachment by a literal "labelId:<id>" path segment.
func (c *Client) RemoveCardLabel(ctx context.Context, cardID, labelID string) error {
	path := "/api/cards/" + pathParam(cardID) + "/card-labels/labelId:" + pathParam(labelID)
	return c.do(ctx, http.MethodDelete, path, nil, nil, nil)
}

// AddCardMember assigns a user to a card.
func (c *Client) AddCardMember(ctx context.Context, cardID, userID string) error {
	body := map[string]string{"userId": userID}
	return c.do(ctx, http.MethodPost, "/api/cards/"+pathParam(cardID)+"/card-memberships", nil, body, nil)
}

// RemoveCardMember unassigns a user from a card. Planka addresses the
// membership by a literal "userId:<id>" path segment.
func (c *Client) RemoveCardMember(ctx context.Context, cardID, userID string) error {
	path := "/api/cards/" + pathParam(cardID) + "/card-memberships/userId:" + pathParam(userID)
	return c.do(ctx, http.MethodDelete, path, nil, nil, nil)
}

// CreateLabel creates a board label. Planka has no default colour, so color is
// always sent.
func (c *Client) CreateLabel(ctx context.Context, boardID, name, color string, position float64) (*Label, error) {
	body := struct {
		Position float64 `json:"position"`
		Name     string  `json:"name"`
		Color    string  `json:"color"`
	}{position, name, color}
	return send[Label](ctx, c, http.MethodPost, "/api/boards/"+pathParam(boardID)+"/labels", body)
}

// UpdateLabelRequest is the body of PATCH /api/labels/:id. Only the fields that
// are Set are sent.
type UpdateLabelRequest struct {
	Name  Optional[string]
	Color Optional[string]
}

// MarshalJSON emits only the Set fields.
func (r UpdateLabelRequest) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	put(m, "name", r.Name)
	put(m, "color", r.Color)
	return json.Marshal(m)
}

// UpdateLabel renames or recolours a board label.
func (c *Client) UpdateLabel(ctx context.Context, id string, r UpdateLabelRequest) (*Label, error) {
	return send[Label](ctx, c, http.MethodPatch, "/api/labels/"+pathParam(id), r)
}

// DeleteLabel deletes a board label. Planka takes it off every card that has
// it.
func (c *Client) DeleteLabel(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/labels/"+pathParam(id), nil, nil, nil)
}

// customFieldValuePath addresses a card's value of a field in a group. Planka
// puts both IDs into one path segment.
func customFieldValuePath(cardID, groupID, fieldID string) string {
	return "/api/cards/" + pathParam(cardID) + "/custom-field-values/customFieldGroupId:" + pathParam(groupID) +
		":customFieldId:" + pathParam(fieldID)
}

// SetCustomFieldValue sets a card's value of a custom field in a group,
// creating or replacing it.
func (c *Client) SetCustomFieldValue(ctx context.Context, cardID, groupID, fieldID, content string) (*CustomFieldValue, error) {
	body := map[string]string{"content": content}
	return send[CustomFieldValue](ctx, c, http.MethodPatch, customFieldValuePath(cardID, groupID, fieldID), body)
}

// ClearCustomFieldValue deletes a card's value of a custom field in a group.
func (c *Client) ClearCustomFieldValue(ctx context.Context, cardID, groupID, fieldID string) error {
	return c.do(ctx, http.MethodDelete, customFieldValuePath(cardID, groupID, fieldID), nil, nil, nil)
}

// CreateComment adds a comment to a card.
func (c *Client) CreateComment(ctx context.Context, cardID, text string) (*Comment, error) {
	body := map[string]string{"text": text}
	return send[Comment](ctx, c, http.MethodPost, "/api/cards/"+pathParam(cardID)+"/comments", body)
}

// UpdateComment replaces a comment's text.
func (c *Client) UpdateComment(ctx context.Context, id, text string) (*Comment, error) {
	body := map[string]string{"text": text}
	return send[Comment](ctx, c, http.MethodPatch, "/api/comments/"+pathParam(id), body)
}

// DeleteComment deletes a comment.
func (c *Client) DeleteComment(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/comments/"+pathParam(id), nil, nil, nil)
}

// CreateTaskList adds a task list (a checklist) to a card.
func (c *Client) CreateTaskList(ctx context.Context, cardID string, r CreateTaskListRequest) (*TaskList, error) {
	return send[TaskList](ctx, c, http.MethodPost, "/api/cards/"+pathParam(cardID)+"/task-lists", r)
}

// UpdateTaskList renames or moves a task list, or changes how it shows.
func (c *Client) UpdateTaskList(ctx context.Context, id string, r UpdateTaskListRequest) (*TaskList, error) {
	return send[TaskList](ctx, c, http.MethodPatch, "/api/task-lists/"+pathParam(id), r)
}

// DeleteTaskList deletes a task list. Planka deletes its tasks with it.
func (c *Client) DeleteTaskList(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/task-lists/"+pathParam(id), nil, nil, nil)
}

// CreateTask adds a task to a task list.
func (c *Client) CreateTask(ctx context.Context, taskListID string, r CreateTaskRequest) (*Task, error) {
	return send[Task](ctx, c, http.MethodPost, "/api/task-lists/"+pathParam(taskListID)+"/tasks", r)
}

// UpdateTask renames, ticks, moves or assigns a task.
func (c *Client) UpdateTask(ctx context.Context, id string, r UpdateTaskRequest) (*Task, error) {
	return send[Task](ctx, c, http.MethodPatch, "/api/tasks/"+pathParam(id), r)
}

// DeleteTask deletes a task.
func (c *Client) DeleteTask(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/tasks/"+pathParam(id), nil, nil, nil)
}
