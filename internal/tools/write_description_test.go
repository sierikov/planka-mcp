package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/sierikov/planka-mcp/internal/service"
)

const longDescription = "Intro.\n\n## Folders\n**Invoice:**\n- Telekom\n\n## Filters\n- Deleted\n"

func getCardWith(t *testing.T, description string, args map[string]any) (text string, isError bool) {
	t.Helper()
	cards := NewMockCardService(t)
	cards.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(&service.Card{
		CardSummary: service.CardSummary{ID: "c1", Title: "T"}, Description: description,
	}, nil)
	args["card"] = "c1"
	res := call(t, connect(t, Deps{Cards: cards}), "get_card", args)
	return resultText(t, res), res.IsError
}

func TestGetCardOutline(t *testing.T) {
	text, isError := getCardWith(t, longDescription, map[string]any{"description": "outline"})

	assert.False(t, isError, text)
	assert.JSONEq(t, `{"id":"c1","title":"T","comments_total":0,"description_version":"`+service.DescriptionVersion(longDescription)+`",`+
		`"description_outline":[{"id":"0","chars":8},{"id":"1","heading":"Folders","level":2,"chars":35},`+
		`{"id":"1.1","heading":"Invoice","level":3,"chars":24},{"id":"2","heading":"Filters","level":2,"chars":21}]}`, text)
}

func TestGetCardOneSection(t *testing.T) {
	text, isError := getCardWith(t, longDescription, map[string]any{"description": "1.1"})

	assert.False(t, isError, text)
	assert.Contains(t, text, `"description":"**Invoice:**\n- Telekom\n\n","section":"1.1"`)
	assert.Contains(t, text, `"description_version":"`+service.DescriptionVersion(longDescription)+`"`, "the version names the whole description")

	text, isError = getCardWith(t, longDescription, map[string]any{"description": "9"})
	assert.True(t, isError)
	assert.Contains(t, text, `description: no section "9"; sections: 0 (intro), 1 Folders, 1.1 Invoice, 2 Filters`)
}

func TestEditDescriptionOutput(t *testing.T) {
	cards := NewMockCardService(t)
	cards.EXPECT().EditDescription(mock.Anything, service.EditDescription{
		Card: service.CardRef{Ref: "c1"}, Version: "abc",
		Edits: []service.DescriptionEdit{{Section: "1.1", Find: new("Telekom"), Replace: new("Vodafone")}},
	}).Return(&service.EditedDescription{
		Card: service.CardSummary{ID: "c1", Title: "T"}, Diff: "@@ -5,1 +5,1 @@\n-- Telekom\n+- Vodafone\n", Version: "def", Sections: []string{"1.1"},
	}, nil)

	res := call(t, connect(t, Deps{Cards: cards}), "edit_description", map[string]any{
		"card": "c1", "version": "abc", "edits": []map[string]any{{"section": "1.1", "find": "Telekom", "replace": "Vodafone"}},
	})

	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"card":{"id":"c1","title":"T"},"diff":"@@ -5,1 +5,1 @@\n-- Telekom\n+- Vodafone\n","description_version":"def","sections":["1.1"]}`,
		resultText(t, res))
}

func TestCutDiff(t *testing.T) {
	diff := strings.Repeat("+line of text\n", 1000)

	kept, hint := cutDiff(diff)

	assert.LessOrEqual(t, len(kept), maxDiffChars)
	assert.True(t, strings.HasSuffix(kept, "\n"))
	assert.Contains(t, hint, "more lines")
}
