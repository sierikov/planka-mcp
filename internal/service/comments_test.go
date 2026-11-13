package service

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka"
)

// commentsFixture wires Comments to mocks; the Cards behind it read board B1.
type commentsFixture struct {
	*cardsFixture
	api      *MockCommentsWriteAPI
	comments *Comments
}

func newCommentsFixture(t *testing.T) *commentsFixture {
	t.Helper()
	f := &commentsFixture{cardsFixture: newCardsFixture(t), api: NewMockCommentsWriteAPI(t)}
	f.comments = NewComments(f.svc, f.api)
	return f
}

func TestAddCommentResolvesTextRef(t *testing.T) {
	f := newCommentsFixture(t)
	f.serve(writeBoard())
	at := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	f.api.EXPECT().CreateComment(mock.Anything, "c1", "  Looks good\n").
		Return(&planka.Comment{ID: "cm1", CardID: "c1", UserID: "u-alice", Text: "  Looks good\n", CreatedAt: at}, nil).Once()

	got, err := f.comments.Add(t.Context(), alphaRef, "  Looks good\n")

	require.NoError(t, err)
	assert.Equal(t, &Comment{ID: "cm1", Author: "Alice", Text: "  Looks good\n", Created: at}, got)
}

func TestAddCommentAuthorFallsBackToMe(t *testing.T) {
	f := newCommentsFixture(t)
	f.serve(writeBoard())
	f.api.EXPECT().CreateComment(mock.Anything, "c1", "hi").
		Return(&planka.Comment{ID: "cm1", UserID: "u-gone", Text: "hi"}, nil).Once()
	f.users.EXPECT().Me(mock.Anything).Return(&planka.User{ID: "u-gone", Name: "Gone Admin", Username: "gone"}, nil).Once()

	got, err := f.comments.Add(t.Context(), alphaRef, "hi")

	require.NoError(t, err)
	assert.Equal(t, "Gone Admin", got.Author)
}

func TestAddCommentWhenMeFailsReturnsTheCommentWithoutAuthor(t *testing.T) {
	f := newCommentsFixture(t)
	f.serve(writeBoard())
	f.api.EXPECT().CreateComment(mock.Anything, "c1", "hi").
		Return(&planka.Comment{ID: "cm1", UserID: "u-gone", Text: "hi"}, nil).Once()
	f.users.EXPECT().Me(mock.Anything).Return(nil, errors.New("boom")).Once()

	got, err := f.comments.Add(t.Context(), alphaRef, "hi")

	// The comment exists: an error would invite a retry and a duplicate.
	require.NoError(t, err)
	assert.Equal(t, &Comment{ID: "cm1", Text: "hi"}, got)
}

func TestAddCommentEmptyTextIsErrorBeforeAnyCall(t *testing.T) {
	f := newCommentsFixture(t)

	for _, text := range []string{"", "  \n\t"} {
		_, err := f.comments.Add(t.Context(), alphaRef, text)
		require.ErrorContains(t, err, "comment text is empty")
	}
}

func TestAddCommentUnknownCard(t *testing.T) {
	f := newCommentsFixture(t)
	f.serve(writeBoard())

	_, err := f.comments.Add(t.Context(), CardRef{Ref: "Nope", Board: "B1"}, "hi")

	var nm *NoMatchError
	require.ErrorAs(t, err, &nm)
}

func TestUpdateComment(t *testing.T) {
	f := newCommentsFixture(t)
	f.api.EXPECT().UpdateComment(mock.Anything, "cm1", "edited").
		Return(&planka.Comment{ID: "cm1", Text: "edited"}, nil).Once()

	got, err := f.comments.Update(t.Context(), " cm1\n", "edited")

	require.NoError(t, err)
	assert.Equal(t, "cm1", got.ID)
	assert.Equal(t, "edited", got.Text)
}

func TestUpdateCommentValidates(t *testing.T) {
	f := newCommentsFixture(t)

	_, err := f.comments.Update(t.Context(), "cm1", " ")
	require.ErrorContains(t, err, "comment text is empty")
	_, err = f.comments.Update(t.Context(), "", "x")
	require.ErrorContains(t, err, "comment ID is empty")
}

func TestUpdateCommentNotFound(t *testing.T) {
	f := newCommentsFixture(t)
	f.api.EXPECT().UpdateComment(mock.Anything, "cm9", "x").
		Return(nil, &planka.APIError{Status: http.StatusNotFound}).Once()

	_, err := f.comments.Update(t.Context(), "cm9", "x")

	assert.ErrorIs(t, err, ErrNotFound)
}

func TestDeleteComment(t *testing.T) {
	f := newCommentsFixture(t)
	f.api.EXPECT().DeleteComment(mock.Anything, "cm1").Return(nil).Once()

	require.NoError(t, f.comments.Delete(t.Context(), " cm1\n"))
	require.ErrorContains(t, f.comments.Delete(t.Context(), " "), "comment ID is empty")
}
