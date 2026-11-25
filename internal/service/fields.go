package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/sierikov/planka-mcp/internal/planka"
)

// maxFieldValue is the longest custom field value Planka stores, in UTF-16
// units.
const maxFieldValue = 512

// CustomFields sets and clears the custom field values of cards. Reading them
// is (*Cards).Get; the fields themselves are made in Planka.
type CustomFields struct {
	cards *Cards
	api   CustomFieldsAPI
}

// NewCustomFields returns a CustomFields service. It finds cards through cards
// and writes through api.
func NewCustomFields(cards *Cards, api CustomFieldsAPI) *CustomFields {
	return &CustomFields{cards: cards, api: api}
}

// fieldWrite is one value to send: content, or a clear when content is empty.
type fieldWrite struct {
	slot    fieldSlot
	name    string // as the error names it: "Group / Field"
	content string
}

// Set sets the card's values of the fields named by the keys of values, and
// clears those whose value is empty (after trimming). A field is named by its
// name, or by "Group / Field" when the name is in several groups; case is
// ignored. Every field is found before the first write, and a value equal to
// the current one is not sent. The result is the card's custom fields as Get
// shows them.
func (s *CustomFields) Set(ctx context.Context, ref CardRef, values map[string]string) ([]CustomFieldGroup, error) {
	if len(values) == 0 {
		return nil, errors.New("fields is empty: give at least one field and its value")
	}
	for name, v := range values {
		if jsLen(strings.TrimSpace(v)) > maxFieldValue {
			return nil, fmt.Errorf("the value of %q is longer than %d characters", name, maxFieldValue)
		}
	}
	id, snap, unlock, err := s.cards.lockCard(ctx, ref)
	if err != nil {
		return nil, err
	}
	defer unlock()
	cr, err := s.cards.cards.Card(ctx, id)
	if err != nil {
		return nil, notFound("card", id, err)
	}
	cat, err := s.cards.fieldCatalog(ctx, cr, snap)
	if err != nil {
		return nil, fmt.Errorf("reading the project's custom field groups: %w", err)
	}
	writes, err := planFieldWrites(cat, cr, values)
	if err != nil {
		return nil, err
	}

	if len(writes) == 0 {
		return cat.groupValues(cr.Included.CustomFieldValues), nil
	}
	done, err := s.write(ctx, id, writes)
	if err != nil {
		return nil, err
	}
	fresh, err := s.cards.cards.Card(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("custom fields %s were written, but reading the card back failed: %w", strings.Join(done, ", "), err)
	}
	return cat.groupValues(fresh.Included.CustomFieldValues), nil
}

// write sends writes in order and returns the names of the fields written. A
// failure after the first write names the fields already written.
func (s *CustomFields) write(ctx context.Context, cardID string, writes []fieldWrite) (done []string, err error) {
	for _, w := range writes {
		g, f := w.slot.group.ID, w.slot.field.ID
		if w.content == "" {
			err = s.api.ClearCustomFieldValue(ctx, cardID, g, f)
		} else {
			_, err = s.api.SetCustomFieldValue(ctx, cardID, g, f, w.content)
		}
		if err != nil {
			if len(done) > 0 {
				return nil, fmt.Errorf("custom fields %s were already written, but %s failed: %w", strings.Join(done, ", "), w.name, err)
			}
			return nil, notFound("card", cardID, err)
		}
		done = append(done, w.name)
	}
	return done, nil
}

// planFieldWrites finds the field of each key and keeps the values that
// change something, in key order.
func planFieldWrites(cat *fieldCatalog, cr *planka.CardResponse, values map[string]string) ([]fieldWrite, error) {
	current := map[[2]string]string{}
	for _, v := range cr.Included.CustomFieldValues {
		current[[2]string{v.CustomFieldGroupID, v.CustomFieldID}] = v.Content
	}
	seen := map[[2]string]string{}
	var writes []fieldWrite
	for _, key := range slices.Sorted(maps.Keys(values)) {
		slot, err := cat.findSlot(cr, key)
		if err != nil {
			return nil, err
		}
		at := [2]string{slot.group.ID, slot.field.ID}
		if other, dup := seen[at]; dup {
			return nil, fmt.Errorf("%q and %q name the same field: give it once", other, key)
		}
		seen[at] = key
		content := strings.TrimSpace(values[key])
		have, set := current[at]
		if (content == "" && !set) || (set && content == have) {
			continue
		}
		writes = append(writes, fieldWrite{slot: slot, name: cat.slotName(slot), content: content})
	}
	return writes, nil
}

// findSlot finds the field a key names: by "Group / Field", or else by the
// field's name alone, exactly but ignoring case. A field name in several
// groups is an AmbiguousError; no match is an UnknownNameError that lists
// every field the card can have.
func (cat *fieldCatalog) findSlot(cr *planka.CardResponse, key string) (fieldSlot, error) {
	q := normalize(key)
	if q == "" {
		return fieldSlot{}, errors.New("a custom field name is empty")
	}
	var full, bare []fieldSlot
	for _, s := range cat.slots {
		switch {
		case normalize(cat.slotName(s)) == q:
			full = append(full, s)
		case normalize(s.field.Name) == q:
			bare = append(bare, s)
		}
	}
	found := full
	if len(found) == 0 {
		found = bare
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		names := make([]string, len(cat.slots))
		for i, s := range cat.slots {
			names[i] = cat.slotName(s)
		}
		return fieldSlot{}, &UnknownNameError{Kind: "custom field", Name: strings.TrimSpace(key), Scope: cardScope(cr), Valid: names}
	}
	cands := make([]Candidate, len(found))
	for i, s := range found {
		cands[i] = Candidate{Label: cat.slotName(s)}
	}
	return fieldSlot{}, &AmbiguousError{Kind: "custom field", Query: strings.TrimSpace(key), Candidates: cands}
}
