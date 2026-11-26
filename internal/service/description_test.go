package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka"
)

// protonLike is shaped like the dogfood card that needed section edits: an
// intro, headings, bold-lead sections and a code fence with a # inside.
const protonLike = `Rules for sorting mail.

## Folders
**Invoice:**
- Fahrschule (16895;
- Telekom

**Newsletters**
Count (842)

## Filters
` + "```" + `
# not a heading
**not bold lead**
` + "```" + `
- Deleted in the UI
`

func TestOutline(t *testing.T) {
	got := Outline(protonLike)

	ids := make([]string, len(got))
	for i, e := range got {
		ids[i] = e.ID + " " + e.Heading
	}
	assert.Equal(t, []string{"0 ", "1 Folders", "1.1 Invoice", "1.2 Newsletters", "2 Filters"}, ids)
	assert.Equal(t, 2, got[1].Level)
	assert.Equal(t, 3, got[2].Level, "a bold lead sits one level below the heading above it")
}

func TestOutlineWithoutIntroOrHeadings(t *testing.T) {
	assert.Empty(t, Outline(""))
	assert.Equal(t, []OutlineEntry{{ID: "0", Chars: 9}}, Outline("just text"))
	got := Outline("# A\n### C\n## B\n")
	assert.Equal(t, []string{"1", "1.1", "1.2"}, []string{got[0].ID, got[1].ID, got[2].ID}, "a skipped level still nests")
}

func TestSection(t *testing.T) {
	got, err := Section(protonLike, "1.2")
	require.NoError(t, err)
	assert.Equal(t, "**Newsletters**\nCount (842)\n\n", got)

	got, err = Section(protonLike, "1")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(got, "## Folders\n**Invoice:**"))
	assert.True(t, strings.HasSuffix(got, "Count (842)\n\n"), "a section holds its subsections")

	_, err = Section(protonLike, "3")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no section "3"; sections: 0 (intro), 1 Folders, 1.1 Invoice, 1.2 Newsletters, 2 Filters`)
}

func TestDescriptionVersion(t *testing.T) {
	assert.Empty(t, DescriptionVersion(""))
	assert.Len(t, DescriptionVersion("x"), 12)
	assert.NotEqual(t, DescriptionVersion("x"), DescriptionVersion("y"))
}

func TestApplyEditsKeepsTheRestByteForByte(t *testing.T) {
	got, changes, err := applyEdits(protonLike, []DescriptionEdit{
		{Section: "1.1", Find: new("16895;"), Replace: new("16895, 16936;")},
		{Section: "1.2", Find: new("(842)"), Replace: new("(805)")},
		{Find: new("- Deleted in the UI"), Replace: new("- Archived\n- Deleted in the UI")},
	})

	require.NoError(t, err)
	assert.Len(t, changes, 3)
	want := strings.NewReplacer("16895;", "16895, 16936;", "(842)", "(805)", "- Deleted in the UI", "- Archived\n- Deleted in the UI").Replace(protonLike)
	assert.Equal(t, want, got)
}

func TestApplyEditsBodyAndAppend(t *testing.T) {
	got, _, err := applyEdits(protonLike, []DescriptionEdit{
		{Section: "1.2", Body: new("Count (805)")},
		{Section: "1.1", Append: new("- Amazon")},
		{Section: "0", Body: new("Rules for sorting mail, v2.")},
	})

	require.NoError(t, err)
	assert.Equal(t, `Rules for sorting mail, v2.

## Folders
**Invoice:**
- Fahrschule (16895;
- Telekom
- Amazon

**Newsletters**
Count (805)

## Filters
`+"```"+`
# not a heading
**not bold lead**
`+"```"+`
- Deleted in the UI
`, got)
}

func TestAppendToTheWholeDescriptionAndToAnEmptySection(t *testing.T) {
	got, _, err := applyEdits("## A\n## B\n", []DescriptionEdit{{Section: "1", Append: new("under A")}, {Append: new("at the end")}})

	require.NoError(t, err)
	assert.Equal(t, "## A\nunder A\n## B\nat the end\n", got)

	got, _, err = applyEdits("## A", []DescriptionEdit{{Section: "1", Body: new("text")}})
	require.NoError(t, err)
	assert.Equal(t, "## A\ntext", got, "a heading on the last line gets its line break")
}

func TestApplyEditsRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		edits []DescriptionEdit
		want  string
	}{
		"none":         {nil, "edits is empty"},
		"two kinds":    {[]DescriptionEdit{{Section: "1", Body: new("x"), Append: new("y")}}, "edit 1: give exactly one of body, append and find"},
		"no replace":   {[]DescriptionEdit{{Find: new("Telekom")}}, "find needs replace"},
		"body no sect": {[]DescriptionEdit{{Body: new("x")}}, "body needs section"},
		"missing":      {[]DescriptionEdit{{Find: new("Vodafone"), Replace: new("")}}, `find "Vodafone" does not occur in the description`},
		"twice":        {[]DescriptionEdit{{Find: new("- "), Replace: new("* ")}}, `occurs 3 times in the description`},
		"out of scope": {[]DescriptionEdit{{Section: "1.2", Find: new("Telekom"), Replace: new("")}}, `does not occur in section 1.2`},
		"unknown":      {[]DescriptionEdit{{Section: "9", Append: new("x")}}, `edit 1: no section "9"`},
		"overlap": {[]DescriptionEdit{
			{Section: "1", Body: new("x")}, {Section: "1.2", Find: new("(842)"), Replace: new("(1)")},
		}, "edits 1 and 2 change the same text"},
		"same point": {[]DescriptionEdit{{Section: "2", Append: new("a")}, {Append: new("b")}}, "change the same text"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := applyEdits(protonLike, tc.edits)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestLineDiff(t *testing.T) {
	a := "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n"
	b := "one\ntwo\nTHREE\nfour\nfive\nsix\nseven\neight\nnine\nten\neleven\n"

	assert.Equal(t, "@@ -1,5 +1,5 @@\n one\n two\n-three\n+THREE\n four\n five\n"+
		"@@ -9,2 +9,3 @@\n nine\n ten\n+eleven\n", lineDiff(a, b))
	assert.Empty(t, lineDiff(a, a))
	assert.Equal(t, "@@ -0,0 +1,1 @@\n+new\n", lineDiff("", "new\n"))
	assert.Equal(t, "@@ -1,2 +1,1 @@\n a\n-b\n", lineDiff("a\nb\n", "a\n"))
}

func TestLineDiffOfAHugeChangeStaysCheap(t *testing.T) {
	a := strings.Repeat("old line\n", 3000)
	b := strings.Repeat("new line\n", 3000)

	got := lineDiff(a, b)

	assert.True(t, strings.HasPrefix(got, "@@ -1,3000 +1,3000 @@\n-old line\n"))
}

// editCard expects the reads of a description edit on Alpha (c1).
func (f *cardsFixture) editCard(description string) {
	f.serveLocked(writeBoard())
	card := cardOf(nil)
	card.Item.Description = &description
	f.cards.EXPECT().Card(mock.Anything, "c1").Return(card, nil).Once()
}

func TestEditDescriptionWritesTheEditsAndReturnsADiff(t *testing.T) {
	f := newCardsFixture(t)
	f.editCard(protonLike)
	want := strings.Replace(protonLike, "(842)", "(805)", 1)
	f.updated(planka.UpdateCardRequest{Description: planka.Some(want)})

	got, err := f.svc.EditDescription(t.Context(), EditDescription{
		Card: alphaRef, Version: DescriptionVersion(protonLike),
		Edits: []DescriptionEdit{{Section: "1.2", Find: new("(842)"), Replace: new("(805)")}},
	})

	require.NoError(t, err)
	assert.Equal(t, "c1", got.Card.ID)
	assert.Equal(t, DescriptionVersion(want), got.Version)
	assert.Equal(t, []string{"1.2"}, got.Sections)
	assert.Contains(t, got.Diff, "-Count (842)\n+Count (805)\n")
}

func TestEditDescriptionRefusesAStaleVersion(t *testing.T) {
	f := newCardsFixture(t)
	f.editCard(protonLike)

	_, err := f.svc.EditDescription(t.Context(), EditDescription{
		Card: alphaRef, Version: "abc123abc123", Edits: []DescriptionEdit{{Append: new("x")}},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "the description changed since you read it (version abc123abc123, now "+DescriptionVersion(protonLike)+")")
}

func TestEditDescriptionThatChangesNothingWritesNothing(t *testing.T) {
	f := newCardsFixture(t)
	f.editCard(protonLike)

	got, err := f.svc.EditDescription(t.Context(), EditDescription{
		Card: alphaRef, Edits: []DescriptionEdit{{Find: new("Telekom"), Replace: new("Telekom")}},
	})

	require.NoError(t, err)
	assert.Empty(t, got.Diff)
	assert.Equal(t, DescriptionVersion(protonLike), got.Version)
}

func TestTouchedSectionsAtTheEnd(t *testing.T) {
	text := "## A\na\n## B\nb\n"
	assert.Equal(t, []string{"2"}, touchedSections(text, []change{{start: len(text), end: len(text)}}))
}
