package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchName(t *testing.T) {
	ident := func(s string) string { return s }

	tests := []struct {
		name    string
		query   string
		items   []string
		want    string
		wantErr any
	}{
		{name: "exact beats prefix", query: "bug", items: []string{"bugfix", "bug"}, want: "bug"},
		{name: "case and whitespace are ignored", query: "  IN PROGRESS ", items: []string{"Open", "In progress"}, want: "In progress"},
		{name: "unique prefix wins", query: "ready", items: []string{"Open", "Ready for release"}, want: "Ready for release"},
		{name: "two exact matches are ambiguous", query: "done", items: []string{"Done", "Open", "DONE"}, wantErr: &AmbiguousError{}},
		{name: "two prefix matches are ambiguous", query: "bug", items: []string{"bugfix", "bugs"}, wantErr: &AmbiguousError{}},
		{name: "a miss is an unknown name", query: "Doing", items: []string{"Open", "Done"}, wantErr: &UnknownNameError{}},
		{name: "an empty query is an unknown name", query: "  ", items: []string{"Open"}, wantErr: &UnknownNameError{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := matchName("list", tc.query, `board "B"`, tc.items, ident, nil)
			switch tc.wantErr.(type) {
			case nil:
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			case *AmbiguousError:
				var amb *AmbiguousError
				require.ErrorAs(t, err, &amb)
				assert.Equal(t, "list", amb.Kind)
				assert.Equal(t, tc.query, amb.Query)
				assert.GreaterOrEqual(t, len(amb.Candidates), 2)
			case *UnknownNameError:
				var unk *UnknownNameError
				require.ErrorAs(t, err, &unk)
				assert.Equal(t, tc.items, unk.Valid, "Valid keeps board order")
				assert.Equal(t, `board "B"`, unk.Scope)
			}
		})
	}
}

func TestMatchNameCandidatesCarryIDs(t *testing.T) {
	type rec struct{ id, name string }
	items := []rec{{"1", "Done"}, {"2", "Open"}, {"3", "done"}}

	_, err := matchName("list", "done", "", items, func(r rec) string { return r.name }, func(r rec) string { return r.id })

	var amb *AmbiguousError
	require.ErrorAs(t, err, &amb)
	assert.Equal(t, []Candidate{{ID: "1", Label: "Done"}, {ID: "3", Label: "done"}}, amb.Candidates)
	assert.Equal(t, `"done" matches several lists: Done (id 1), done (id 3)`, amb.Error())
}

func TestErrorStrings(t *testing.T) {
	unk := &UnknownNameError{
		Kind: "list", Name: "Doing", Scope: `board "Planka MCP"`,
		Valid: []string{"Open", "In progress", "Ready for release", "Done", "Dropped"},
	}
	assert.Equal(t,
		`no list "Doing" on board "Planka MCP"; lists: Open, In progress, Ready for release, Done, Dropped`,
		unk.Error())

	amb := &AmbiguousError{Kind: "board", Query: "Inbox", Candidates: []Candidate{
		{ID: "1", Label: "Personal / Inbox"}, {ID: "2", Label: "Work / Inbox"},
	}}
	assert.Equal(t, `"Inbox" matches several boards: Personal / Inbox (id 1), Work / Inbox (id 2)`, amb.Error())

	assert.Equal(t, `"Done" matches several lists: Done, DONE`,
		(&AmbiguousError{Kind: "list", Query: "Done", Candidates: []Candidate{{Label: "Done"}, {Label: "DONE"}}}).Error())

	assert.Equal(t, `no card matching "x" on board "B"; archived cards were not searched, set include_archived to search the archive`,
		(&NoMatchError{Kind: "card", Query: "x", Scope: `board "B"`, ArchiveHint: true}).Error())
	assert.Equal(t, `no card matching "x"`, (&NoMatchError{Kind: "card", Query: "x"}).Error())
}

func TestParseRef(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		kind   string
		wantID string
		wantOK bool
	}{
		{name: "numeric id", in: "1887084132131931341", kind: "cards", wantID: "1887084132131931341", wantOK: true},
		{name: "id with spaces", in: " 42 ", kind: "cards", wantID: "42", wantOK: true},
		{name: "card url", in: "https://planka.example.com/cards/123?x=1", kind: "cards", wantID: "123", wantOK: true},
		{name: "board url", in: "https://planka.example.com/boards/77", kind: "boards", wantID: "77", wantOK: true},
		{name: "board url asked as card", in: "https://planka.example.com/boards/77", kind: "cards"},
		{name: "plain text", in: "backup script", kind: "cards"},
		{name: "url without id", in: "https://planka.example.com/cards/abc", kind: "cards"},
		{name: "empty", in: "", kind: "cards"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := parseRef(tc.in, tc.kind)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantID, id)
		})
	}
}
