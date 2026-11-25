package planka

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
)

// CreateBoard creates a board in a project at the given position. Planka
// takes it only as a multipart form, and only from a project manager.
func (c *Client) CreateBoard(ctx context.Context, projectID, name string, position float64) (*Board, error) {
	body, err := multipartForm([][2]string{{"position", strconv.FormatFloat(position, 'f', -1, 64)}, {"name", name}}, nil)
	if err != nil {
		return nil, err
	}
	return send[Board](ctx, c, http.MethodPost, "/api/projects/"+pathParam(projectID)+"/boards", body)
}

// UpdateBoard renames a board.
func (c *Client) UpdateBoard(ctx context.Context, id, name string) (*Board, error) {
	body := map[string]string{"name": name}
	return send[Board](ctx, c, http.MethodPatch, "/api/boards/"+pathParam(id), body)
}

// DeleteBoard deletes a board with its lists and cards.
func (c *Client) DeleteBoard(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/boards/"+pathParam(id), nil, nil, nil)
}

// BoardMembershipRequest is the body of a board membership create or update.
// Only the fields that are Set are sent; a Null CanComment takes Planka's
// default.
type BoardMembershipRequest struct {
	UserID     Optional[string]
	Role       Optional[string]
	CanComment Optional[bool]
}

// MarshalJSON emits only the Set fields.
func (r BoardMembershipRequest) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	put(m, "userId", r.UserID)
	put(m, "role", r.Role)
	put(m, "canComment", r.CanComment)
	return json.Marshal(m)
}

// CreateBoardMembership gives a user access to a board. r needs UserID and
// Role.
func (c *Client) CreateBoardMembership(ctx context.Context, boardID string, r BoardMembershipRequest) (*BoardMembership, error) {
	return send[BoardMembership](ctx, c, http.MethodPost, "/api/boards/"+pathParam(boardID)+"/board-memberships", r)
}

// UpdateBoardMembership changes a member's role or whether a viewer may
// comment.
func (c *Client) UpdateBoardMembership(ctx context.Context, id string, r BoardMembershipRequest) (*BoardMembership, error) {
	return send[BoardMembership](ctx, c, http.MethodPatch, "/api/board-memberships/"+pathParam(id), r)
}

// DeleteBoardMembership takes a user's access to a board away.
func (c *Client) DeleteBoardMembership(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/board-memberships/"+pathParam(id), nil, nil, nil)
}

// Users lists every user. Planka allows it to admins and project owners only.
func (c *Client) Users(ctx context.Context) ([]User, error) {
	var out struct {
		Items []User `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/users", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}
