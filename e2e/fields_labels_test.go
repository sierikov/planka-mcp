//go:build e2e

package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetCustomFields fills in, changes and clears a board-level field and a
// card-level field on a real Planka, reading the card back with get_card, and
// finds an empty field under unset.
func TestSetCustomFields(t *testing.T) {
	p, b := newFlowBoard(t)
	card := p.newCard(t, b, "Open", "Senior engineer interview")
	group := p.send(t, http.MethodPost, "/boards/"+b.ID+"/custom-field-groups", map[string]any{
		"position": positionGap, "name": "Interview",
	}).Item.ID
	for i, name := range []string{"Date of Stage 1", "Stage"} {
		p.send(t, http.MethodPost, "/custom-field-groups/"+group+"/custom-fields", map[string]any{"position": positionGap * (i + 1), "name": name})
	}
	notes := p.send(t, http.MethodPost, "/cards/"+card+"/custom-field-groups", map[string]any{"position": positionGap, "name": "Notes"}).Item.ID
	p.send(t, http.MethodPost, "/custom-field-groups/"+notes+"/custom-fields", map[string]any{"position": positionGap, "name": "Contact"})
	s := connectStdio(t, env.key)
	read := func() []customFieldGroup {
		return callInto[cardDetail](t, s, "get_card", map[string]any{"card": card}).CustomFields
	}

	empty := read()
	require.Len(t, empty, 2)
	assert.Equal(t, []string{"Date of Stage 1", "Stage"}, empty[0].Unset)
	assert.Equal(t, []string{"Contact"}, empty[1].Unset)

	set := callInto[struct {
		CustomFields []customFieldGroup `json:"custom_fields"`
	}](t, s, "set_custom_fields", map[string]any{"card": card, "fields": map[string]any{
		"date of stage 1": "5/11/2026", "Notes / Contact": "Ann",
	}})
	require.Len(t, set.CustomFields, 2)
	require.Len(t, set.CustomFields[0].Fields, 1)
	assert.Equal(t, "5/11/2026", set.CustomFields[0].Fields[0].Value)
	assert.Equal(t, []string{"Stage"}, set.CustomFields[0].Unset)

	callInto[struct{}](t, s, "set_custom_fields", map[string]any{"card": card, "fields": map[string]any{
		"Date of Stage 1": "6/11/2026", "Contact": "",
	}})
	got := read()
	require.Len(t, got, 2)
	assert.Equal(t, "6/11/2026", got[0].Fields[0].Value)
	assert.Empty(t, got[1].Fields)
	assert.Equal(t, []string{"Contact"}, got[1].Unset)

	text, isError := call(t, s, "set_custom_fields", map[string]any{"card": card, "fields": map[string]any{"Date": "1"}})
	assert.True(t, isError, text)
	assert.Contains(t, text, `no custom field "Date"`)
	assert.Contains(t, text, "Interview / Date of Stage 1")
}

// boardLabels reads a board's labels straight from Planka: name to colour.
func (p *planka) boardLabels(t *testing.T, b *board) map[string]string {
	t.Helper()
	var out struct {
		Included struct {
			Labels []struct {
				Name  string `json:"name"`
				Color string `json:"color"`
			} `json:"labels"`
		} `json:"included"`
	}
	require.NoError(t, p.request(t.Context(), http.MethodGet, "/boards/"+b.ID, "", nil, &out))
	labels := map[string]string{}
	for _, l := range out.Included.Labels {
		labels[l.Name] = l.Color
	}
	return labels
}

// TestLabelTools creates a label through create_card, which picks its colour,
// renames and recolours it, and deletes it: refused while it is on a card and
// an archived card, then with from_cards.
func TestLabelTools(t *testing.T) {
	p, b := newFlowBoard(t)
	s := connectStdio(t, env.key)
	callInto[writeResult](t, s, "create_card", map[string]any{"board": b.ID, "title": "Flaky test", "labels": []string{"triage"}, "create_labels": true})
	old := callInto[writeResult](t, s, "create_card", map[string]any{"board": b.ID, "title": "Old flaky test", "labels": []string{"triage"}})
	callInto[writeResult](t, s, "move_card", map[string]any{"card": old.Card.ID, "list": "archive"})
	color := p.boardLabels(t, b)["triage"]
	assert.NotEmpty(t, color)
	assert.NotEqual(t, "muddy-grey", color, "a created label gets a colour picked from its name")

	updated := callInto[struct {
		Name       string `json:"name"`
		Color      string `json:"color"`
		CardsTotal int    `json:"cards_total"`
	}](t, s, "update_label", map[string]any{"board": b.Name, "label": "TRIAGE", "name": "Triage", "color": "berry-red"})
	assert.Equal(t, "Triage", updated.Name)
	assert.Equal(t, "berry-red", updated.Color)
	assert.Equal(t, 1, updated.CardsTotal, "the archived card is not counted")
	assert.Equal(t, map[string]string{"Triage": "berry-red"}, p.boardLabels(t, b))

	text, isError := call(t, s, "update_label", map[string]any{"board": b.Name, "label": "Triage", "color": "red"})
	assert.True(t, isError, text)

	text, isError = call(t, s, "delete_label", map[string]any{"board": b.Name, "label": "Triage"})
	assert.True(t, isError, text)
	assert.Contains(t, text, `label "Triage" is on 2 cards`)

	deleted := callInto[struct {
		Deleted string     `json:"deleted"`
		Cards   []cardLine `json:"cards"`
	}](t, s, "delete_label", map[string]any{"board": b.Name, "label": "Triage", "from_cards": true})
	require.Len(t, deleted.Cards, 2)
	assert.Equal(t, "Flaky test", deleted.Cards[0].Title)
	assert.Equal(t, "(archived)", deleted.Cards[1].List)
	assert.Empty(t, p.boardLabels(t, b))
}

// TestCreateLabelTool makes labels without a card, in a picked colour and in
// one given, and get_board shows them with their colours.
func TestCreateLabelTool(t *testing.T) {
	p, b := newFlowBoard(t)
	s := connectStdio(t, env.key)

	picked := callInto[struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Color string `json:"color"`
	}](t, s, "create_label", map[string]any{"board": b.Name, "name": "Blocked"})
	assert.NotEmpty(t, picked.ID)
	assert.NotEqual(t, "muddy-grey", picked.Color, "a colour is picked from the name")

	given := callInto[struct{ Color string }](t, s, "create_label", map[string]any{"board": b.ID, "name": "Urgent", "color": "berry-red"})
	assert.Equal(t, "berry-red", given.Color)

	text, isError := call(t, s, "create_label", map[string]any{"board": b.ID, "name": "urgent"})
	assert.True(t, isError, text)
	assert.Contains(t, text, `already has a label "Urgent"`)

	assert.Equal(t, map[string]string{"Blocked": picked.Color, "Urgent": "berry-red"}, p.boardLabels(t, b))
	board := callInto[struct {
		Labels []struct {
			Name  string `json:"name"`
			Color string `json:"color"`
		} `json:"labels"`
	}](t, s, "get_board", map[string]any{"board": b.ID})
	require.Len(t, board.Labels, 2)
	assert.Equal(t, "Blocked", board.Labels[0].Name)
	assert.Equal(t, picked.Color, board.Labels[0].Color)
	assert.Equal(t, "Urgent", board.Labels[1].Name)
	assert.Equal(t, "berry-red", board.Labels[1].Color)
}

// TestSearchMatchesAtWordStarts: "oma" finds a word that starts with it, in
// the title or (marked) in the description, and not "Automated".
func TestSearchMatchesAtWordStarts(t *testing.T) {
	p, b := newFlowBoard(t)
	p.newCard(t, b, "Open", "Migrate Calibre Web Automated")
	p.newCard(t, b, "Open", "Omas Geburtstag")
	doctor := p.newCard(t, b, "Open", "Arzttermin")
	p.send(t, http.MethodPatch, "/cards/"+doctor, map[string]any{"description": "mit Oma"})

	found := callInto[searchResult](t, connectStdio(t, env.key), "search_cards", map[string]any{"query": "oma", "board": b.ID})

	require.Len(t, found.Cards, 2)
	assert.Equal(t, "Omas Geburtstag", found.Cards[0].Title)
	assert.False(t, found.Cards[0].InDescription)
	assert.Equal(t, "Arzttermin", found.Cards[1].Title)
	assert.True(t, found.Cards[1].InDescription)
}
