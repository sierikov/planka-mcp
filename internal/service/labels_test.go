package service

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka"
)

type labelsFixture struct {
	*cardsFixture
	api    *MockBoardLabelsAPI
	labels *Labels
}

func newLabelsFixture(t *testing.T) *labelsFixture {
	t.Helper()
	f := &labelsFixture{cardsFixture: newCardsFixture(t), api: NewMockBoardLabelsAPI(t)}
	f.labels = NewLabels(f.svc, f.api)
	return f
}

// labelBoard is writeBoard: Alpha (c1) has the label bug, Beta and Gamma have none.
func (f *labelsFixture) serveBoard() {
	f.projects.EXPECT().Projects(mock.Anything).Return(synthProjects(), nil)
	f.boards.EXPECT().Board(mock.Anything, "b1").Return(writeBoard(), nil).Once()
}

// archived answers the archive's label filter with one page of the given
// cards; a page shorter than Planka's is the last.
func (f *labelsFixture) archived(labelID string, cards ...planka.Card) {
	page := &planka.CardsResponse{Items: cards}
	f.cards.EXPECT().ListCards(mock.Anything, "b1-arch", mock.MatchedBy(func(q planka.CardQuery) bool {
		return len(q.LabelIDs) == 1 && q.LabelIDs[0] == labelID && q.Before == nil
	})).Return(page, nil).Once()
}

// fullPage is Planka's page of archived cards, all with a label.
func fullPage() []planka.Card {
	out := make([]planka.Card, plankaCardsPage)
	for i := range out {
		out[i] = planka.Card{ID: fmt.Sprint(9000 + i), BoardID: "b1", ListID: "b1-arch", Name: fmt.Sprint("Old ", i), ListChangedAt: time.Now()}
	}
	return out
}

func TestLabelColorForIsStableAndNeverGrey(t *testing.T) {
	assert.Equal(t, labelColorFor("Bug"), labelColorFor(" bug "))
	seen := map[string]bool{}
	for i := range 200 {
		c := labelColorFor(fmt.Sprintf("label-%d", i))
		assert.NotEqual(t, "muddy-grey", c)
		assert.Contains(t, LabelColors, c)
		seen[c] = true
	}
	assert.Greater(t, len(seen), 20, "names spread over the palette")
}

func TestUpdateLabelRenamesAndRecolours(t *testing.T) {
	f := newLabelsFixture(t)
	f.serveBoard()
	f.api.EXPECT().UpdateLabel(mock.Anything, "b1-bug", planka.UpdateLabelRequest{Name: planka.Some("Bug"), Color: planka.Some("berry-red")}).
		Return(&planka.Label{ID: "b1-bug", Name: new("Bug"), Color: "berry-red"}, nil).Once()

	got, err := f.labels.Update(t.Context(), UpdateLabel{Board: "B1", Label: "BUG", Name: new(" Bug "), Color: new("berry-red")})

	require.NoError(t, err)
	assert.Equal(t, &LabelResult{ID: "b1-bug", Name: "Bug", Color: "berry-red", Cards: 1}, got)
}

func TestUpdateLabelSendsOnlyWhatChanges(t *testing.T) {
	f := newLabelsFixture(t)
	f.serveBoard()

	// The name is the same and no colour is given: nothing is sent.
	got, err := f.labels.Update(t.Context(), UpdateLabel{Board: "B1", Label: "ui", Name: new("ui")})

	require.NoError(t, err)
	assert.Equal(t, "ui", got.Name)
	assert.Zero(t, got.Cards)
}

// TestUpdateLabelRefusesBeforeWriting: no write is expected, so none may
// happen.
func TestUpdateLabelRefusesBeforeWriting(t *testing.T) {
	for name, tc := range map[string]struct {
		in   UpdateLabel
		read bool
		want string
	}{
		"nothing":       {UpdateLabel{Board: "B1", Label: "bug"}, false, "nothing to change"},
		"empty name":    {UpdateLabel{Board: "B1", Label: "bug", Name: new(" ")}, false, "label name is empty"},
		"unknown color": {UpdateLabel{Board: "B1", Label: "bug", Color: new("red")}, false, `unknown label color "red"; colors: muddy-grey`},
		"no board":      {UpdateLabel{Label: "bug", Color: new("berry-red")}, false, "board is empty"},
		"unknown label": {UpdateLabel{Board: "B1", Label: "bu", Color: new("berry-red")}, true, `no label "bu" on board "B1"; labels: bug, ui`},
		"name taken":    {UpdateLabel{Board: "B1", Label: "bug", Name: new("UI")}, true, `already has a label "ui"`},
	} {
		t.Run(name, func(t *testing.T) {
			f := newLabelsFixture(t)
			if tc.read {
				f.serveBoard()
			}

			_, err := f.labels.Update(t.Context(), tc.in)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestDeleteLabelInUseIsRefused(t *testing.T) {
	f := newLabelsFixture(t)
	f.serveBoard()
	f.archived("b1-bug")

	_, err := f.labels.Delete(t.Context(), "B1", "bug", false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `label "bug" is on 1 cards`)
	assert.Contains(t, err.Error(), "from_cards=true")
}

func TestDeleteLabelOnlyOnArchivedCardsIsRefusedToo(t *testing.T) {
	f := newLabelsFixture(t)
	f.serveBoard()
	f.archived("b1-ui", planka.Card{ID: "c9", BoardID: "b1", ListID: "b1-arch", Name: "Old", ListChangedAt: time.Now()})

	_, err := f.labels.Delete(t.Context(), "B1", "ui", false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `label "ui" is on 1 cards`)
}

func TestDeleteLabelFromCardsListsThem(t *testing.T) {
	f := newLabelsFixture(t)
	f.serveBoard()
	f.archived("b1-bug", planka.Card{ID: "c9", BoardID: "b1", ListID: "b1-arch", Name: "Old", ListChangedAt: time.Now()})
	f.api.EXPECT().DeleteLabel(mock.Anything, "b1-bug").Return(nil).Once()

	got, err := f.labels.Delete(t.Context(), "B1", "bug", true)

	require.NoError(t, err)
	assert.Equal(t, "b1-bug", got.ID)
	assert.Equal(t, []string{"Alpha", "Old"}, titles(got.Cards))
	assert.Equal(t, "(archived)", got.Cards[1].List)
}

func TestDeleteUnusedLabel(t *testing.T) {
	f := newLabelsFixture(t)
	f.serveBoard()
	f.archived("b1-ui")
	f.api.EXPECT().DeleteLabel(mock.Anything, "b1-ui").Return(nil).Once()

	got, err := f.labels.Delete(t.Context(), "B1", "ui", false)

	require.NoError(t, err)
	assert.Empty(t, got.Cards)
}

func TestDeleteLabelGoneMeanwhileIsNotFound(t *testing.T) {
	f := newLabelsFixture(t)
	f.serveBoard()
	f.archived("b1-ui")
	f.api.EXPECT().DeleteLabel(mock.Anything, "b1-ui").Return(&planka.APIError{Status: http.StatusNotFound}).Once()

	_, err := f.labels.Delete(t.Context(), "B1", "ui", false)

	require.ErrorIs(t, err, ErrNotFound)
}

// TestDeleteLabelSurvivesAFailingSecondArchivePage: Planka 2.1.1 answers 500
// to the before cursor. The cards read so far still count, and the result
// says there may be more.
func TestDeleteLabelSurvivesAFailingSecondArchivePage(t *testing.T) {
	f := newLabelsFixture(t)
	f.serveBoard()
	f.archived("b1-ui", fullPage()...)
	f.cards.EXPECT().ListCards(mock.Anything, "b1-arch", mock.MatchedBy(func(q planka.CardQuery) bool { return q.Before != nil })).
		Return(nil, &planka.APIError{Status: http.StatusInternalServerError}).Once()

	_, err := f.labels.Delete(t.Context(), "B1", "ui", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `label "ui" is on 50 or more cards`)
}

func TestDeleteLabelReadsFurtherArchivePages(t *testing.T) {
	f := newLabelsFixture(t)
	f.serveBoard()
	f.archived("b1-bug", fullPage()...)
	f.cards.EXPECT().ListCards(mock.Anything, "b1-arch", mock.MatchedBy(func(q planka.CardQuery) bool { return q.Before != nil })).
		Return(&planka.CardsResponse{Items: []planka.Card{{ID: "9999", BoardID: "b1", ListID: "b1-arch", Name: "Oldest"}}}, nil).Once()
	f.api.EXPECT().DeleteLabel(mock.Anything, "b1-bug").Return(nil).Once()

	got, err := f.labels.Delete(t.Context(), "B1", "bug", true)

	require.NoError(t, err)
	assert.Len(t, got.Cards, maxLabelCards)
	assert.Equal(t, 2, got.More, "Alpha, 50 on the first page and 1 on the second: 52 in all")
	assert.False(t, got.Partial)
}
