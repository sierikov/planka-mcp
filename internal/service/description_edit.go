package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sierikov/planka-mcp/internal/planka"
)

// EditDescription changes parts of Card's description (see DescriptionEdit).
// Version, when given, must be the DescriptionVersion of the description as
// it is: section IDs shift when headings change, so an edit planned on an
// older text is refused.
type EditDescription struct {
	Card    CardRef
	Version string
	Edits   []DescriptionEdit
}

// EditedDescription is a description after an edit: a unified diff of the
// change, its new version, and the outline IDs of the sections the edits
// landed in. Diff is empty when the edits changed nothing.
type EditedDescription struct {
	Card     CardSummary
	Diff     string
	Version  string
	Sections []string
}

// EditDescription applies edits to a card's description, all of them found in
// the description as it is, or none: a find that is missing or not unique,
// or edits that overlap, write nothing.
func (c *Cards) EditDescription(ctx context.Context, in EditDescription) (*EditedDescription, error) {
	if len(in.Edits) == 0 {
		return nil, errors.New("edits is empty: give at least one edit")
	}
	id, snap, unlock, err := c.lockCard(ctx, in.Card)
	if err != nil {
		return nil, err
	}
	defer unlock()
	cr, err := c.cards.Card(ctx, id)
	if err != nil {
		return nil, notFound("card", id, err)
	}
	old := deref(cr.Item.Description)
	current := DescriptionVersion(old)
	if v := strings.TrimSpace(in.Version); v != "" && v != current {
		return nil, fmt.Errorf("the description changed since you read it (version %s, now %s): read it again with get_card", v, cmpOrNone(current))
	}
	updated, changes, err := applyEdits(old, in.Edits)
	if err != nil {
		return nil, err
	}
	out := &EditedDescription{Card: cardSummary(snap.overlay(cardOverlay(cr)), id), Version: current}
	if updated == old {
		return out, nil
	}
	if _, err := c.write.UpdateCard(ctx, id, planka.UpdateCardRequest{Description: planka.Some(updated)}); err != nil {
		return nil, fmt.Errorf("writing the description of card %s failed, so nothing was changed: %w", id, err)
	}
	out.Diff = lineDiff(old, updated)
	out.Version = DescriptionVersion(updated)
	out.Sections = touchedSections(updated, changes)
	return out, nil
}

// touchedSections is the outline IDs, in the new text, of the innermost
// sections the changes landed in, in order and without repeats.
func touchedSections(text string, changes []change) []string {
	secs := sections(text)
	var out []string
	delta := 0
	for _, ch := range changes {
		at := ch.start + delta
		delta += len(ch.with) - (ch.end - ch.start)
		id := ""
		for _, s := range secs {
			if s.start <= at && (at < s.end || (at == s.end && s.end == len(text))) {
				id = s.ID // later sections are deeper or later: the last match is the innermost
			}
		}
		if id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// cmpOrNone names an empty version.
func cmpOrNone(version string) string {
	if version == "" {
		return "none: the description is empty"
	}
	return version
}
