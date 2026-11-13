package service

import (
	"context"
	"errors"
	"strings"

	"github.com/sierikov/planka-mcp/internal/planka"
)

// Comments adds, changes and deletes comments on cards. Reading them is
// (*Cards).Comments.
type Comments struct {
	cards *Cards
	api   CommentsWriteAPI
}

// NewComments returns a Comments service. It finds cards through cards and
// writes through api.
func NewComments(cards *Cards, api CommentsWriteAPI) *Comments {
	return &Comments{cards: cards, api: api}
}

// Add posts a comment on a card. The text is sent as given; empty or
// whitespace-only text is an error. The comment comes back with the author's
// name: the key's own user, found on the card's board or, when the user is not
// a member of it, through Me. If that lookup fails the author is empty and the
// call still succeeds, because the comment was posted.
func (c *Comments) Add(ctx context.Context, ref CardRef, text string) (*Comment, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("comment text is empty")
	}
	id, snap, err := c.cards.resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	cm, err := c.api.CreateComment(ctx, id, text)
	if err != nil {
		return nil, notFound("card", id, err)
	}
	out := toComment(cm)
	// The comment exists now. A failed author lookup leaves the author empty
	// rather than failing the call, which would invite a retry and a duplicate.
	out.Author = c.author(ctx, snap, cm.UserID)
	return &out, nil
}

// Update replaces the text of a comment, found by its ID. Empty or
// whitespace-only text is an error.
func (c *Comments) Update(ctx context.Context, id, text string) (*Comment, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("comment ID is empty")
	}
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("comment text is empty")
	}
	cm, err := c.api.UpdateComment(ctx, id, text)
	if err != nil {
		return nil, notFound("comment", id, err)
	}
	out := toComment(cm)
	return &out, nil
}

// Delete deletes a comment, found by its ID.
func (c *Comments) Delete(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("comment ID is empty")
	}
	return notFound("comment", id, c.api.DeleteComment(ctx, id))
}

// author names the user with the given ID: from the board's users, else Me.
// It is empty when Me fails.
func (c *Comments) author(ctx context.Context, snap *boardSnapshot, userID string) string {
	if u, ok := snap.users[userID]; ok {
		return userName(u)
	}
	me, err := c.cards.users.Me(ctx)
	if err != nil {
		return ""
	}
	return userName(*me)
}

// toComment converts an API comment; the author is left for the caller.
func toComment(cm *planka.Comment) Comment {
	return Comment{ID: cm.ID, Text: cm.Text, Created: cm.CreatedAt}
}
