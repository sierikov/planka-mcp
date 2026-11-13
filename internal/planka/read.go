package planka

import (
	"context"
	"net/http"
	"net/url"
)

// cursorTimeLayout is the ISO 8601 form Planka itself emits for timestamps.
const cursorTimeLayout = "2006-01-02T15:04:05.000Z"

// Me returns the user the API key belongs to.
func (c *Client) Me(ctx context.Context) (*User, error) {
	var out struct {
		Item User `json:"item"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/users/me", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out.Item, nil
}

// Projects lists the projects the user can see, with their boards.
func (c *Client) Projects(ctx context.Context) (*ProjectsResponse, error) {
	var out ProjectsResponse
	if err := c.do(ctx, http.MethodGet, "/api/projects", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Board returns a board with its lists, cards, labels and tasks. Archived
// cards are not included.
func (c *Client) Board(ctx context.Context, id string) (*BoardResponse, error) {
	var out BoardResponse
	if err := c.do(ctx, http.MethodGet, "/api/boards/"+pathParam(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Card returns one card with its labels, members, tasks and attachments.
func (c *Client) Card(ctx context.Context, id string) (*CardResponse, error) {
	var out CardResponse
	if err := c.do(ctx, http.MethodGet, "/api/cards/"+pathParam(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListCards returns one page of a list's cards. It is the only way to read
// the cards of an archive list.
func (c *Client) ListCards(ctx context.Context, listID string, q CardQuery) (*CardsResponse, error) {
	query := url.Values{}
	if q.Search != "" {
		query.Set("search", q.Search)
	}
	if q.Before != nil {
		query.Set("before[listChangedAt]", q.Before.ListChangedAt.UTC().Format(cursorTimeLayout))
		query.Set("before[id]", q.Before.ID)
	}
	var out CardsResponse
	if err := c.do(ctx, http.MethodGet, "/api/lists/"+pathParam(listID)+"/cards", query, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Comments returns a card's comments, newest first. A non-empty beforeID asks
// for the page of comments older than that comment.
func (c *Client) Comments(ctx context.Context, cardID, beforeID string) (*CommentsResponse, error) {
	query := url.Values{}
	if beforeID != "" {
		query.Set("beforeId", beforeID)
	}
	var out CommentsResponse
	if err := c.do(ctx, http.MethodGet, "/api/cards/"+pathParam(cardID)+"/comments", query, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
