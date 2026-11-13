package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sierikov/planka-mcp/internal/planka"
)

// CreateCard describes a new card. Board is an ID, a URL or a name. List
// defaults to the board's first active list. Labels that the board lacks are
// created. Due is "2006-01-02", "2006-01-02 15:04" or RFC 3339. Position is
// "top" (the default) or "bottom".
type CreateCard struct {
	Board, List, Title, Description string
	Labels, Assignees               []string
	// CreateLabels lets a label name the board lacks be created; without it
	// such a name is an error, so a typo never becomes a new label.
	CreateLabels bool
	Due          string
	Position     string
}

// MoveCard moves a card to a list. Card.Board only narrows the search for the
// card; ToBoard is the destination board, empty to stay on the card's own
// board. List may be "archive". Position is "top" (the default) or "bottom".
type MoveCard struct {
	Card                    CardRef
	List, ToBoard, Position string
}

// UpdateCard changes a card. A nil field and an empty list or Stopwatch are
// left alone; an empty Description clears the description and an empty Due
// clears the due date. Labels that the board lacks are created. Stopwatch is
// "start", "stop" or "reset".
type UpdateCard struct {
	Card                          CardRef
	Title, Description            *string
	Due                           *string
	DueCompleted                  *bool
	AddLabels, RemoveLabels       []string
	CreateLabels                  bool // as in CreateCard
	AddAssignees, RemoveAssignees []string
	Stopwatch                     string
}

// WriteResult is the card as it is after a write. CreatedLabels names the
// board labels the write had to create.
type WriteResult struct {
	Card          CardSummary
	CreatedLabels []string
}

// Create adds a card to a list. The labels and assignees are applied after the
// card exists.
func (c *Cards) Create(ctx context.Context, in CreateCard) (*WriteResult, error) {
	if strings.TrimSpace(in.Title) == "" {
		return nil, errors.New("card title is empty")
	}
	if strings.TrimSpace(in.Board) == "" {
		return nil, errors.New("board is empty")
	}
	due, err := optionalDue(in.Due)
	if err != nil {
		return nil, err
	}
	br, err := c.catalog.resolveBoard(ctx, in.Board)
	if err != nil {
		return nil, err
	}
	snap, unlock, err := c.lockBoard(ctx, br.ID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	list, err := firstOrNamedList(snap, in.List)
	if err != nil {
		return nil, err
	}
	pos, err := pickPosition(in.Position, listPositions(snap, list.ID, ""))
	if err != nil {
		return nil, err
	}
	plan, err := c.planAssociations(ctx, snap, "", assocInput{addLabels: in.Labels, createLabels: in.CreateLabels, addUsers: in.Assignees})
	if err != nil {
		return nil, err
	}

	req := planka.CreateCardRequest{Type: snap.resp.Item.DefaultCardType, Position: pos, Name: in.Title, DueDate: due}
	if req.Type == "" {
		req.Type = "project"
	}
	if in.Description != "" {
		req.Description = &in.Description
	}
	card, err := c.write.CreateCard(ctx, list.ID, req)
	if err != nil {
		return nil, err
	}
	created, err := c.applyAssociations(ctx, snap, card.ID, plan)
	if err != nil {
		return nil, fmt.Errorf("card %s was created%s, but: %w", card.ID, appliedNote(false, created), err)
	}
	return c.written(ctx, snap, card.ID, created, "created")
}

// Move puts a card in a list, at the top or the bottom of it. Moving to
// another board needs ToBoard; moving to "archive" archives the card. Only the
// destination board is locked: the move plans nothing from the board it
// leaves, and holding both could deadlock two moves in opposite directions.
func (c *Cards) Move(ctx context.Context, in MoveCard) (*WriteResult, error) {
	id, boardID, err := c.cardBoard(ctx, in.Card)
	if err != nil {
		return nil, err
	}
	var req planka.UpdateCardRequest
	if strings.TrimSpace(in.ToBoard) != "" {
		br, err := c.catalog.resolveBoard(ctx, in.ToBoard)
		if err != nil {
			return nil, err
		}
		if br.ID != boardID {
			boardID, req.BoardID = br.ID, planka.Some(br.ID)
		}
	}
	dest, unlock, err := c.lockBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	list, err := dest.listByName(in.List)
	if err != nil {
		return nil, err
	}
	pos, err := pickPosition(in.Position, listPositions(dest, list.ID, id))
	if err != nil {
		return nil, err
	}
	if list.Type == archiveWord {
		pos = archivePosition
	}
	req.ListID, req.Position = planka.Some(list.ID), planka.Some(pos)
	if _, err := c.write.UpdateCard(ctx, id, req); err != nil {
		return nil, err
	}
	return c.written(ctx, dest, id, nil, "moved")
}

// Update changes a card's fields, labels, assignees and stopwatch. Every name
// is resolved before the first write; the writes are the fields, then the
// labels, then the assignees. Adding what is there or removing what is not
// makes no call.
func (c *Cards) Update(ctx context.Context, in UpdateCard) (*WriteResult, error) {
	patch, err := c.validatePatch(in)
	if err != nil {
		return nil, err
	}
	id, snap, unlock, err := c.lockCard(ctx, in.Card)
	if err != nil {
		return nil, err
	}
	defer unlock()
	cur, err := c.withCard(ctx, id, snap)
	if err != nil {
		return nil, err
	}
	plan, err := c.planAssociations(ctx, snap, id, assocInput{
		addLabels: in.AddLabels, removeLabels: in.RemoveLabels, createLabels: in.CreateLabels,
		addUsers: in.AddAssignees, removeUsers: in.RemoveAssignees,
		onCard: cur,
	})
	if err != nil {
		return nil, err
	}
	if patch, err = c.withStopwatch(patch, in.Stopwatch, cur.cards[id].Stopwatch); err != nil {
		return nil, err
	}

	patched := patch != (planka.UpdateCardRequest{})
	if patched {
		if _, err := c.write.UpdateCard(ctx, id, patch); err != nil {
			return nil, err
		}
	}
	created, err := c.applyAssociations(ctx, snap, id, plan)
	if err != nil {
		if note := appliedNote(patched, created); note != "" {
			return nil, fmt.Errorf("card %s was partly updated%s, but: %w", id, note, err)
		}
		return nil, err
	}
	return c.written(ctx, snap, id, created, "updated")
}

// Delete permanently deletes a card and returns the summary it had.
func (c *Cards) Delete(ctx context.Context, ref CardRef) (*CardSummary, error) {
	id, snap, err := c.resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	cur, err := c.withCard(ctx, id, snap)
	if err != nil {
		return nil, err
	}
	out := cardSummary(cur, id)
	if err := c.write.DeleteCard(ctx, id); err != nil {
		return nil, err
	}
	return &out, nil
}

// written reads the board again and returns the card as it is now. The write
// already happened (verb says which: created, moved or updated), so a failed
// read says so; otherwise a retry would repeat the write.
func (c *Cards) written(ctx context.Context, snap *boardSnapshot, cardID string, created []string, verb string) (*WriteResult, error) {
	fresh, err := c.readBack(ctx, snap, cardID)
	if err != nil {
		return nil, fmt.Errorf("card %s was %s, but reading it back failed: %w", cardID, verb, err)
	}
	return &WriteResult{Card: cardSummary(fresh, cardID), CreatedLabels: created}, nil
}

// readBack reads the board again and returns it with the card on it.
func (c *Cards) readBack(ctx context.Context, snap *boardSnapshot, cardID string) (*boardSnapshot, error) {
	resp, err := c.boards.Board(ctx, snap.resp.Item.ID)
	if err != nil {
		return nil, err
	}
	return c.withCard(ctx, cardID, newSnapshot(resp, snap.project))
}

// withCard returns snap, or when the card is not among its cards (an archived
// card is not), a snapshot that has the card read on its own.
func (c *Cards) withCard(ctx context.Context, id string, snap *boardSnapshot) (*boardSnapshot, error) {
	if _, ok := snap.cards[id]; ok {
		return snap, nil
	}
	cr, err := c.cards.Card(ctx, id)
	if err != nil {
		return nil, notFound("card", id, err)
	}
	return snap.overlay(cardOverlay(cr)), nil
}
