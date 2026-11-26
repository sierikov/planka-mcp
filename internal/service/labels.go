package service

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"strings"

	"github.com/sierikov/planka-mcp/internal/planka"
)

// LabelColors are the colours Planka 2.1 and 2.2 accept for a label.
var LabelColors = []string{
	"muddy-grey", "autumn-leafs", "morning-sky", "antique-blue", "egg-yellow", "desert-sand", "dark-granite",
	"fresh-salad", "lagoon-blue", "midnight-blue", "light-orange", "pumpkin-orange", "light-concrete",
	"sunny-grass", "navy-blue", "lilac-eyes", "apricot-red", "orange-peel", "silver-glint", "bright-moss",
	"deep-ocean", "summer-sky", "berry-red", "light-cocoa", "grey-stone", "tank-green", "coral-green",
	"sugar-plum", "pink-tulip", "shady-rust", "wet-rock", "wet-moss", "turquoise-sea", "lavender-fields",
	"piggy-red", "light-mud", "gun-metal", "modern-green", "french-coast", "sweet-lilac", "red-burgundy",
	"pirate-gold",
}

const (
	// maxLabelName is the longest label name Planka stores, in UTF-16 units.
	maxLabelName = 128
	// maxLabelCards is how many cards a DeletedLabel lists; More counts the rest.
	maxLabelCards = 50
	// maxArchivePages bounds the archive pages read for a label's cards.
	maxArchivePages = 20
	// plankaCardsPage is how many cards Planka returns per ListCards page.
	plankaCardsPage = 50
)

// labelColorFor picks the colour of a label a write creates: always the same
// for the same name, and never muddy-grey, the colour of a label nobody chose.
func labelColorFor(name string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(normalize(name)))
	colors := LabelColors[1:]
	return colors[h.Sum32()%uint32(len(colors))] //nolint:gosec // len(colors) is 41, far below uint32's range
}

// Labels creates, changes and deletes the labels of a board. Card writes also
// create labels (create_labels); reading them is (*Catalog).GetBoard.
type Labels struct {
	cards *Cards
	api   BoardLabelsAPI
}

// NewLabels returns a Labels service. It finds boards and holds their write
// locks through cards, and writes through api.
func NewLabels(cards *Cards, api BoardLabelsAPI) *Labels {
	return &Labels{cards: cards, api: api}
}

// CreateLabel describes a new label on Board. A nil Color picks one from the
// name, as create_labels does.
type CreateLabel struct {
	Board, Name string
	Color       *string
}

// UpdateLabel changes the label Label (exact name or ID) on Board. A nil
// field is left alone. Color is one of LabelColors.
type UpdateLabel struct {
	Board, Label string
	Name, Color  *string
}

// LabelResult is a label after a write. Cards counts the cards in the board's
// lists that have it; archived cards are not counted.
type LabelResult struct {
	ID, Name, Color string
	Cards           int
}

// DeletedLabel is a deleted label and the cards it was taken off, archived
// ones included: at most maxLabelCards of them, More counts the rest. Partial
// says Planka could not list every archived card, so there may be more.
type DeletedLabel struct {
	ID, Name, Color string
	Cards           []CardSummary
	More            int
	Partial         bool
}

// Create adds a label after the board's other labels. Its name must differ
// from theirs, case ignored.
func (s *Labels) Create(ctx context.Context, in CreateLabel) (*LabelResult, error) {
	name, err := checkLabelName(in.Name)
	if err != nil {
		return nil, err
	}
	color := labelColorFor(name)
	if in.Color != nil {
		if err := checkLabelColor(*in.Color); err != nil {
			return nil, err
		}
		color = *in.Color
	}
	snap, unlock, err := s.cards.lockBoardRef(ctx, in.Board)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := uniqueLabelName(snap, name, ""); err != nil {
		return nil, err
	}
	positions := make([]float64, len(snap.resp.Included.Labels))
	for i, l := range snap.resp.Included.Labels {
		positions[i] = l.Position
	}
	created, err := s.api.CreateLabel(ctx, snap.resp.Item.ID, name, color, bottomPosition(positions))
	if err != nil {
		return nil, err
	}
	return &LabelResult{ID: created.ID, Name: labelName(*created), Color: created.Color}, nil
}

// Update renames or recolours a label. A new name must differ from the
// board's other labels, case ignored.
func (s *Labels) Update(ctx context.Context, in UpdateLabel) (*LabelResult, error) {
	patch, err := checkLabelUpdate(in)
	if err != nil {
		return nil, err
	}
	snap, unlock, err := s.cards.lockBoardRef(ctx, in.Board)
	if err != nil {
		return nil, err
	}
	defer unlock()
	target, err := targetLabel(snap, in.Label)
	if err != nil {
		return nil, err
	}
	if patch.Name.Set {
		if err := uniqueLabelName(snap, patch.Name.Value, target.ID); err != nil {
			return nil, err
		}
		if target.Name != nil && *target.Name == patch.Name.Value {
			patch.Name = planka.Optional[string]{}
		}
	}
	if patch.Color.Set && patch.Color.Value == target.Color {
		patch.Color = planka.Optional[string]{}
	}
	out := target
	if patch != (planka.UpdateLabelRequest{}) {
		updated, err := s.api.UpdateLabel(ctx, target.ID, patch)
		if err != nil {
			return nil, notFound("label", target.ID, err)
		}
		out = *updated
	}
	return &LabelResult{ID: out.ID, Name: labelName(out), Color: out.Color, Cards: len(snap.cardsWithLabel(target.ID))}, nil
}

// Delete deletes a label. Planka takes it off every card that has it and
// keeps no record of which, so a label still on cards is refused unless
// fromCards is set; the result lists those cards.
func (s *Labels) Delete(ctx context.Context, board, label string, fromCards bool) (*DeletedLabel, error) {
	snap, unlock, err := s.cards.lockBoardRef(ctx, board)
	if err != nil {
		return nil, err
	}
	defer unlock()
	target, err := targetLabel(snap, label)
	if err != nil {
		return nil, err
	}
	cards, more, partial, err := s.labelCards(ctx, snap, target.ID)
	if err != nil {
		return nil, err
	}
	if n := len(cards) + more; n > 0 && !fromCards {
		count := fmt.Sprint(n)
		if partial {
			count += " or more"
		}
		return nil, fmt.Errorf("label %q is on %s cards, and deleting it takes it off them for good: "+
			"set from_cards=true to delete it anyway", labelName(target), count)
	}
	if err := s.api.DeleteLabel(ctx, target.ID); err != nil {
		return nil, notFound("label", target.ID, err)
	}
	return &DeletedLabel{ID: target.ID, Name: labelName(target), Color: target.Color, Cards: cards, More: more, Partial: partial}, nil
}

// labelCards is the cards that have the label: those in the board's lists,
// then the archived ones, at most maxLabelCards; more counts the rest. The
// archive is read a page at a time. Planka 2.1.1 fails every page after the
// first (it answers 500 to the before cursor), so a failed later page ends the
// reading and partial says the archive may hold more.
func (s *Labels) labelCards(ctx context.Context, snap *boardSnapshot, labelID string) (cards []CardSummary, more int, partial bool, err error) {
	cards = snap.cardsWithLabel(labelID)
	if arch := snap.archiveList(); arch != nil {
		q := planka.CardQuery{LabelIDs: []string{labelID}}
		for i := range maxArchivePages {
			page, err := s.cards.cards.ListCards(ctx, arch.ID, q)
			if err != nil && i == 0 {
				return nil, 0, false, err
			}
			if err != nil {
				partial = true
				break
			}
			view := snap.overlay(planka.BoardIncluded{
				Cards: page.Items, CardLabels: page.Included.CardLabels,
				CardMemberships: page.Included.CardMemberships, Users: page.Included.Users,
			})
			for _, c := range page.Items {
				cards = append(cards, cardSummary(view, c.ID))
			}
			if len(page.Items) < plankaCardsPage {
				break
			}
			if i == maxArchivePages-1 {
				partial = true
			}
			last := page.Items[len(page.Items)-1]
			q.Before = &planka.CardCursor{ListChangedAt: last.ListChangedAt, ID: last.ID}
		}
	}
	if len(cards) > maxLabelCards {
		return cards[:maxLabelCards], len(cards) - maxLabelCards, partial, nil
	}
	return cards, 0, partial, nil
}

// checkLabelUpdate checks an update before anything is read, and returns the
// PATCH it asks for.
func checkLabelUpdate(in UpdateLabel) (planka.UpdateLabelRequest, error) {
	var patch planka.UpdateLabelRequest
	if in.Name == nil && in.Color == nil {
		return patch, errors.New("nothing to change: give name or color")
	}
	if in.Name != nil {
		name, err := checkLabelName(*in.Name)
		if err != nil {
			return patch, err
		}
		patch.Name = planka.Some(name)
	}
	if in.Color != nil {
		if err := checkLabelColor(*in.Color); err != nil {
			return patch, err
		}
		patch.Color = planka.Some(*in.Color)
	}
	return patch, nil
}

// checkLabelName trims a label name and checks it is not empty or too long.
func checkLabelName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", errors.New("label name is empty")
	case jsLen(name) > maxLabelName:
		return "", fmt.Errorf("label name is longer than %d characters", maxLabelName)
	}
	return name, nil
}

// checkLabelColor checks a colour is one of LabelColors.
func checkLabelColor(color string) error {
	if !slices.Contains(LabelColors, color) {
		return fmt.Errorf("unknown label color %q; colors: %s", color, strings.Join(LabelColors, ", "))
	}
	return nil
}

// targetLabel finds the label a label write targets with exactLabel: by ID,
// or by its exact name (an unnamed label by its colour). No match is an
// UnknownNameError that lists the board's labels.
func targetLabel(snap *boardSnapshot, ref string) (planka.Label, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return planka.Label{}, errors.New("label is empty: give its name or ID")
	}
	l, ok, err := exactLabel(snap, ref)
	if err != nil || ok {
		return l, err
	}
	labels := snap.sortedLabels()
	names := make([]string, len(labels))
	for i, l := range labels {
		names[i] = labelName(l)
	}
	return planka.Label{}, &UnknownNameError{Kind: "label", Name: ref, Scope: snap.scope(), Valid: names}
}

// uniqueLabelName refuses a name another label on the board has, case
// ignored; skip is the label being renamed.
func uniqueLabelName(snap *boardSnapshot, name, skip string) error {
	for _, l := range snap.resp.Included.Labels {
		if l.ID != skip && l.Name != nil && normalize(*l.Name) == normalize(name) {
			return fmt.Errorf("%s already has a label %q (id %s): pick another name", snap.scope(), *l.Name, l.ID)
		}
	}
	return nil
}
