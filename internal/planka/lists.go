package planka

import (
	"context"
	"encoding/json"
	"net/http"
)

// CreateListRequest is the body of POST /api/boards/:boardId/lists. Type is
// "active" or "closed". Planka takes no colour here: set it with UpdateList.
type CreateListRequest struct {
	Type     string  `json:"type"`
	Position float64 `json:"position"`
	Name     string  `json:"name"`
}

// UpdateListRequest is the body of PATCH /api/lists/:id. Only the fields that
// are Set are sent; a Null Color clears the colour.
type UpdateListRequest struct {
	Type     Optional[string]
	Position Optional[float64]
	Name     Optional[string]
	Color    Optional[string]
}

// MarshalJSON emits only the Set fields, as null where Null is true.
func (r UpdateListRequest) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	put(m, "type", r.Type)
	put(m, "position", r.Position)
	put(m, "name", r.Name)
	put(m, "color", r.Color)
	return json.Marshal(m)
}

// CreateList adds a list to a board.
func (c *Client) CreateList(ctx context.Context, boardID string, r CreateListRequest) (*List, error) {
	return send[List](ctx, c, http.MethodPost, "/api/boards/"+pathParam(boardID)+"/lists", r)
}

// UpdateList renames, retypes, moves or recolours a list.
func (c *Client) UpdateList(ctx context.Context, id string, r UpdateListRequest) (*List, error) {
	return send[List](ctx, c, http.MethodPatch, "/api/lists/"+pathParam(id), r)
}

// movedCards is Planka's answer to a list delete or move-cards: the cards it moved.
type movedCards struct {
	Included struct {
		Cards []Card `json:"cards"`
	} `json:"included"`
}

// DeleteList deletes a list and returns the cards Planka moved to the board's
// trash list.
func (c *Client) DeleteList(ctx context.Context, id string) ([]Card, error) {
	var out movedCards
	if err := c.do(ctx, http.MethodDelete, "/api/lists/"+pathParam(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Included.Cards, nil
}

// SortList reorders the cards of a list by field ("name", "dueDate" or
// "createdAt"), in order "asc" or "desc".
func (c *Client) SortList(ctx context.Context, id, field, order string) error {
	body := map[string]string{"fieldName": field, "order": order}
	return c.do(ctx, http.MethodPost, "/api/lists/"+pathParam(id)+"/sort", nil, body, nil)
}

// MoveListCards moves every card of a list to another list and returns the
// cards it moved. Planka allows this only from a closed list to the board's
// archive list.
func (c *Client) MoveListCards(ctx context.Context, id, toListID string) ([]Card, error) {
	body := map[string]string{"listId": toListID}
	var out movedCards
	if err := c.do(ctx, http.MethodPost, "/api/lists/"+pathParam(id)+"/move-cards", nil, body, &out); err != nil {
		return nil, err
	}
	return out.Included.Cards, nil
}
