package service

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka"
)

type fieldsFixture struct {
	*cardsFixture
	api    *MockCustomFieldsAPI
	fields *CustomFields
}

func newFieldsFixture(t *testing.T) *fieldsFixture {
	t.Helper()
	f := &fieldsFixture{cardsFixture: newCardsFixture(t), api: NewMockCustomFieldsAPI(t)}
	f.fields = NewCustomFields(f.svc, f.api)
	return f
}

// fieldBoard is writeBoard with two board groups, Interview (Date, Stage) and
// Offer (Date).
func fieldBoard() *planka.BoardResponse {
	b := writeBoard()
	b.Included.CustomFieldGroups = []planka.CustomFieldGroup{
		{ID: "gi", BoardID: new("b1"), Name: new("Interview"), Position: 1},
		{ID: "go", BoardID: new("b1"), Name: new("Offer"), Position: 2},
	}
	b.Included.CustomFields = []planka.CustomField{
		{ID: "fi-date", CustomFieldGroupID: "gi", Name: "Date", Position: 1},
		{ID: "fi-stage", CustomFieldGroupID: "gi", Name: "Stage", Position: 2},
		{ID: "fo-date", CustomFieldGroupID: "go", Name: "Date", Position: 1},
	}
	return b
}

// fieldCard is card c1 with its own group Notes (Contact) and the values given.
func fieldCard(values ...planka.CustomFieldValue) *planka.CardResponse {
	r := cardOf(nil)
	r.Included.CustomFieldGroups = []planka.CustomFieldGroup{{ID: "gn", CardID: new("c1"), Name: new("Notes"), Position: 1}}
	r.Included.CustomFields = []planka.CustomField{{ID: "fn-contact", CustomFieldGroupID: "gn", Name: "Contact", Position: 1}}
	r.Included.CustomFieldValues = values
	return r
}

func value(group, field, content string) planka.CustomFieldValue {
	return planka.CustomFieldValue{ID: "v-" + field, CardID: "c1", CustomFieldGroupID: group, CustomFieldID: field, Content: content}
}

// locked expects the reads of a field write on c1 up to its first write.
func (f *fieldsFixture) locked(b *planka.BoardResponse, card *planka.CardResponse) {
	f.serveLocked(b)
	f.cards.EXPECT().Card(mock.Anything, "c1").Return(card, nil).Once()
}

func TestSetCustomFieldsSetsClearsAndSkipsUnchanged(t *testing.T) {
	f := newFieldsFixture(t)
	f.locked(fieldBoard(), fieldCard(value("gi", "fi-stage", "2"), value("gn", "fn-contact", "Ann")))
	f.api.EXPECT().SetCustomFieldValue(mock.Anything, "c1", "gi", "fi-date", "2026-11-06").
		Return(&planka.CustomFieldValue{}, nil).Once()
	f.api.EXPECT().ClearCustomFieldValue(mock.Anything, "c1", "gn", "fn-contact").Return(nil).Once()
	after := fieldCard(value("gi", "fi-date", "2026-11-06"), value("gi", "fi-stage", "2"))
	f.cards.EXPECT().Card(mock.Anything, "c1").Return(after, nil).Once()

	got, err := f.fields.Set(t.Context(), alphaRef, map[string]string{
		"interview / date": " 2026-11-06 ", "Stage": "2", "contact": "", "Offer / Date": "",
	})

	require.NoError(t, err)
	assert.Equal(t, []CustomFieldGroup{
		{Name: "Interview", Fields: []CustomField{{Name: "Date", Value: "2026-11-06"}, {Name: "Stage", Value: "2"}}},
		{Name: "Offer", Unset: []string{"Date"}},
		{Name: "Notes", Unset: []string{"Contact"}},
	}, got)
}

func TestSetCustomFieldsWithNothingToChangeWritesNothing(t *testing.T) {
	f := newFieldsFixture(t)
	f.locked(fieldBoard(), fieldCard(value("gi", "fi-stage", "2")))

	got, err := f.fields.Set(t.Context(), alphaRef, map[string]string{"stage": "2"})

	require.NoError(t, err)
	assert.Equal(t, "Interview", got[0].Name)
}

func TestSetCustomFieldsOfABaseGroup(t *testing.T) {
	f := newFieldsFixture(t)
	b := writeBoard()
	b.Included.CustomFieldGroups = []planka.CustomFieldGroup{{ID: "gb", BoardID: new("b1"), BaseCustomFieldGroupID: new("base1")}}
	projects := synthProjects()
	projects.Included.BaseCustomFieldGroups = []planka.BaseCustomFieldGroup{{ID: "base1", Name: "Backup"}}
	projects.Included.CustomFields = []planka.CustomField{{ID: "bf-tool", BaseCustomFieldGroupID: new("base1"), Name: "Tool"}}
	f.projects.EXPECT().Projects(mock.Anything).Return(projects, nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(b, nil).Times(2)
	f.cards.EXPECT().Card(mock.Anything, "c1").Return(cardOf(nil), nil).Once()
	f.api.EXPECT().SetCustomFieldValue(mock.Anything, "c1", "gb", "bf-tool", "Zerobyte").Return(&planka.CustomFieldValue{}, nil).Once()
	after := cardOf(nil)
	after.Included.CustomFieldValues = []planka.CustomFieldValue{value("gb", "bf-tool", "Zerobyte")}
	f.cards.EXPECT().Card(mock.Anything, "c1").Return(after, nil).Once()

	got, err := f.fields.Set(t.Context(), alphaRef, map[string]string{"Backup / Tool": "Zerobyte"})

	require.NoError(t, err)
	assert.Equal(t, []CustomFieldGroup{{Name: "Backup", Fields: []CustomField{{Name: "Tool", Value: "Zerobyte"}}}}, got)
}

func TestSetCustomFieldsSaysWhatWasWrittenBeforeAFailure(t *testing.T) {
	f := newFieldsFixture(t)
	f.locked(fieldBoard(), fieldCard())
	f.api.EXPECT().SetCustomFieldValue(mock.Anything, "c1", "gi", "fi-date", "1").Return(&planka.CustomFieldValue{}, nil).Once()
	f.api.EXPECT().SetCustomFieldValue(mock.Anything, "c1", "gi", "fi-stage", "2").
		Return(nil, &planka.APIError{Status: http.StatusBadGateway}).Once()

	_, err := f.fields.Set(t.Context(), alphaRef, map[string]string{"Interview / Date": "1", "Stage": "2"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "custom fields Interview / Date were already written, but Interview / Stage failed")
}

// TestSetCustomFieldsRefusesBeforeWriting: no write is expected, so none may
// happen.
func TestSetCustomFieldsRefusesBeforeWriting(t *testing.T) {
	for name, tc := range map[string]struct {
		values map[string]string
		read   bool
		want   string
	}{
		"empty":     {map[string]string{}, false, "fields is empty"},
		"too long":  {map[string]string{"Stage": strings.Repeat("x", 513)}, false, `the value of "Stage" is longer than 512`},
		"ambiguous": {map[string]string{"date": "1"}, true, `"date" matches several custom fields: Interview / Date; Offer / Date`},
		"unknown":   {map[string]string{"Dat": "1"}, true, `no custom field "Dat" on card "Alpha"; custom fields: Interview / Date, Interview / Stage, Offer / Date, Notes / Contact`},
		"twice":     {map[string]string{"Stage": "1", "Interview / Stage": "2"}, true, `name the same field`},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFieldsFixture(t)
			if tc.read {
				f.locked(fieldBoard(), fieldCard())
			}

			_, err := f.fields.Set(t.Context(), alphaRef, tc.values)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestGetCardListsUnsetFields(t *testing.T) {
	f := newCardsFixture(t)
	card := fixtureCard(t)
	card.Included.CustomFieldValues = []planka.CustomFieldValue{
		{ID: "v1", CardID: fixtureReadmeID, CustomFieldGroupID: "bg1", CustomFieldID: "bf2", Content: "Zerobyte"},
	}
	board := fixtureBoard(t)
	board.Included.CustomFieldGroups = []planka.CustomFieldGroup{
		{ID: "bg1", BoardID: new(fixtureBoardID), Name: new("Backup"), Position: 1},
		{ID: "bg2", BoardID: new(fixtureBoardID), Name: new("Empty group"), Position: 2},
		{ID: "bg3", BoardID: new(fixtureBoardID), Name: new("Unused"), Position: 3},
	}
	board.Included.CustomFields = []planka.CustomField{
		{ID: "bf1", CustomFieldGroupID: "bg1", Name: "Schedule", Position: 1},
		{ID: "bf2", CustomFieldGroupID: "bg1", Name: "Tool", Position: 2},
		{ID: "bf3", CustomFieldGroupID: "bg3", Name: "Owner", Position: 1},
	}
	f.serveCard(t, card, board)

	got, err := f.svc.Get(t.Context(), CardRef{Ref: fixtureReadmeID}, 20)

	require.NoError(t, err)
	assert.Equal(t, []CustomFieldGroup{
		{Name: "Backup", Fields: []CustomField{{Name: "Tool", Value: "Zerobyte"}}, Unset: []string{"Schedule"}},
		{Name: "Unused", Unset: []string{"Owner"}},
	}, got.CustomFields, "a group without fields is left out")
}
