package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/service"
)

// statusErr stands in for planka.APIError, which tools may not import.
type statusErr int

func (e statusErr) Error() string   { return fmt.Sprintf("planka: status %d", int(e)) }
func (e statusErr) StatusCode() int { return int(e) }

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok, "content is %T", res.Content[0])
	return text.Text
}

func TestFormatDate(t *testing.T) {
	midnight := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)
	afternoon := time.Date(2026, 10, 20, 14, 30, 0, 0, time.Local)

	assert.Equal(t, "2026-10-20", formatDate(midnight))
	assert.Equal(t, "2026-10-20 14:30", formatDate(afternoon))
	// The same instant given in UTC is shown in local time.
	assert.Equal(t, "2026-10-20 14:30", formatDate(afternoon.UTC()))
}

func TestCardLineOmitsEmpty(t *testing.T) {
	raw, err := json.Marshal(cardLine{ID: "1", Title: "x"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"1","title":"x"}`, string(raw))
}

func TestCardLineShape(t *testing.T) {
	due := time.Date(2026, 10, 20, 9, 0, 0, 0, time.Local)
	line := toCardLine(service.CardSummary{
		ID: "7", Title: "Fix", Board: "B", List: "Open", Labels: []string{"bug"}, Assignees: []string{"Ann"},
		Due: &due, DueCompleted: true, TasksDone: 1, TasksTotal: 3, CommentsTotal: 4,
	})
	raw, err := json.Marshal(line)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"7","title":"Fix","board":"B","list":"Open","labels":["bug"],"assignees":["Ann"],
		"due":"2026-10-20 09:00","due_done":true,"tasks":"1/3","comments_total":4}`, string(raw))
}

func TestCardOutShape(t *testing.T) {
	out := toCardOut(&service.Card{
		CardSummary: service.CardSummary{ID: "1", Title: "T", Board: "B", List: "L", CommentsTotal: 1},
		Project:     "P", Type: "project", Description: "desc",
		Stopwatch: &service.Stopwatch{Total: 90 * time.Minute, Running: true},
		TaskLists: []service.TaskList{{Name: "Tasks", Tasks: []service.Task{
			{ID: "t1", Name: "a", Completed: true},
			{ID: "t2", Name: "b", Linked: &service.CardSummary{ID: "9", Title: "Child", List: "Open"}},
		}}},
		Attachments: []service.Attachment{{Name: "f.txt", URL: "http://x/f.txt"}},
		CustomFields: []service.CustomFieldGroup{
			{Name: "Meta", Fields: []service.CustomField{{Name: "Size", Value: "L", ShowOnFrontOfCard: true}, {Name: "Note", Value: "a, b"}}},
			{Fields: []service.CustomField{{Name: "Owner", Value: "Ann"}}},
		},
		Comments: []service.Comment{{ID: "c1", Author: "Ann", Text: "hi", Created: time.Date(2026, 10, 1, 8, 5, 0, 0, time.Local)}},
	})
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"1","title":"T","board":"B","list":"L","project":"P","type":"project","description":"desc",
		"stopwatch":"1h 30m (running)",
		"task_lists":[{"name":"Tasks","tasks":[{"id":"t1","name":"a","done":true},
			{"id":"t2","name":"b","link":{"id":"9","title":"Child","list":"Open"}}]}],
		"attachments":[{"name":"f.txt","url":"http://x/f.txt"}],
		"custom_fields":[
			{"group":"Meta","fields":[{"name":"Size","value":"L","show_on_front_of_card":true},{"name":"Note","value":"a, b"}]},
			{"fields":[{"name":"Owner","value":"Ann"}]}],
		"comments_total":1,
		"comments":[{"id":"c1","author":"Ann","date":"2026-10-01 08:05","text":"hi"}]}`, string(raw))
}

func TestCardOutSaysWhenThereAreNoComments(t *testing.T) {
	raw, err := json.Marshal(toCardOut(&service.Card{CardSummary: service.CardSummary{ID: "1", Title: "T"}}))
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"1","title":"T","comments_total":0}`, string(raw))
}

func TestStopwatchText(t *testing.T) {
	assert.Equal(t, "0m", stopwatchText(&service.Stopwatch{}))
	assert.Equal(t, "45m", stopwatchText(&service.Stopwatch{Total: 45 * time.Minute}))
	assert.Equal(t, "1h 30m", stopwatchText(&service.Stopwatch{Total: 90 * time.Minute}))
	assert.Equal(t, "2h 0m (running)", stopwatchText(&service.Stopwatch{Total: 2 * time.Hour, Running: true}))
	assert.Empty(t, stopwatchText(nil))
}

func TestErrorResultAmbiguous(t *testing.T) {
	res, out, err := errorResult(fmt.Errorf("resolve: %w", &service.AmbiguousError{
		Kind: "card", Query: "login",
		Candidates: []service.Candidate{{ID: "111", Label: "Login bug / Board A / Open"}, {ID: "222", Label: "Login page / Board B / Done"}},
	}))
	require.NoError(t, err)
	assert.Nil(t, out)
	assert.True(t, res.IsError)
	text := resultText(t, res)
	assert.Contains(t, text, "111")
	assert.Contains(t, text, "222")
	assert.Contains(t, text, "Login bug / Board A / Open")
}

// TestErrorResultAmbiguousListShowsIDs: two lists with one name can only be told apart by ID, so
// the error shows each ID and says to retry with one.
func TestErrorResultAmbiguousListShowsIDs(t *testing.T) {
	res, _, _ := errorResult(&service.AmbiguousError{Kind: "list", Query: "Done", Candidates: []service.Candidate{
		{ID: "1887", Label: "Done"}, {ID: "1888", Label: "Done"},
	}})
	assert.Equal(t, `"Done" matches several lists: Done (id 1887); Done (id 1888); retry with the id of the one you mean`, resultText(t, res))
}

func TestErrorResultAmbiguousWithoutIDs(t *testing.T) {
	res, _, _ := errorResult(&service.AmbiguousError{Kind: "list", Query: "Done", Candidates: []service.Candidate{{Label: "A / Done"}, {Label: "B / Done"}}})
	text := resultText(t, res)
	assert.Contains(t, text, "A / Done")
	assert.Contains(t, text, "B / Done")
	assert.NotContains(t, text, "id )")
	assert.Contains(t, text, "retry with one of them")
}

func TestErrorResultUnknownName(t *testing.T) {
	res, _, err := errorResult(&service.UnknownNameError{Kind: "list", Name: "Doing", Scope: `board "X"`, Valid: []string{"Open", "In progress", "Done"}})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	text := resultText(t, res)
	for _, name := range []string{"Open", "In progress", "Done", "Doing"} {
		assert.Contains(t, text, name)
	}
}

func TestErrorResultNoMatch(t *testing.T) {
	res, _, _ := errorResult(&service.NoMatchError{Kind: "card", Query: "zzz", Scope: `board "X"`, ArchiveHint: true})
	assert.Contains(t, resultText(t, res), "set include_archived to also search the archive")

	res, _, _ = errorResult(&service.NoMatchError{Kind: "card", Query: "zzz"})
	assert.NotContains(t, resultText(t, res), "include_archived")
}

func TestErrorResultNotFound(t *testing.T) {
	res, _, _ := errorResult(fmt.Errorf("card 5: %w", service.ErrNotFound))
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "card 5")
	assert.Contains(t, resultText(t, res), "not found")
}

func TestErrorResultAuth(t *testing.T) {
	res, _, err := errorResult(statusErr(401))
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Equal(t, "Planka refused the credentials (401): the API key or login is wrong or has expired. In stdio mode they come from PLANKA_API_KEY or PLANKA_USERNAME and PLANKA_PASSWORD; in http mode the client sends the key as \"Authorization: Bearer <key>\"", resultText(t, res))

	res, _, _ = errorResult(statusErr(403))
	assert.True(t, res.IsError)
	assert.Equal(t, "Planka refused (403): you lack rights on this board, or Planka does not allow this change", resultText(t, res))

	// Wrapped, the context is kept and the hint follows it.
	res, _, _ = errorResult(fmt.Errorf("load board: %w", statusErr(401)))
	assert.Equal(t, "load board: planka: status 401; Planka refused the credentials (401): the API key or login is wrong or has expired. In stdio mode they come from PLANKA_API_KEY or PLANKA_USERNAME and PLANKA_PASSWORD; in http mode the client sends the key as \"Authorization: Bearer <key>\"",
		resultText(t, res))
}

// TestErrorResultKeepsPartialWriteContext: when a write fails halfway, the error says what was
// already done, so a retry does not repeat it. The hint for the status must not replace that.
func TestErrorResultKeepsPartialWriteContext(t *testing.T) {
	res, _, _ := errorResult(fmt.Errorf("card 123 was created, but: %w", fmt.Errorf("add label %q: %w", "bug", statusErr(403))))
	assert.True(t, res.IsError)
	assert.Equal(t, `card 123 was created, but: add label "bug": planka: status 403; Planka refused (403): you lack rights on this board, or Planka does not allow this change`,
		resultText(t, res))

	// A wrapped name error keeps its context too, and is rendered once.
	res, _, _ = errorResult(fmt.Errorf("item 2: %w", &service.AmbiguousError{
		Kind: "card", Query: "login", Candidates: []service.Candidate{{ID: "1", Label: "A"}, {ID: "2", Label: "B"}},
	}))
	assert.Equal(t, `item 2: "login" matches several cards: A (id 1); B (id 2); retry with the id of the one you mean`, resultText(t, res))

	res, _, _ = errorResult(fmt.Errorf("card 9 was partly updated (fields updated), but: %w",
		&service.UnknownNameError{Kind: "user", Name: "Bob", Scope: `board "X"`, Valid: []string{"Ann"}}))
	assert.Equal(t, `card 9 was partly updated (fields updated), but: no user "Bob" on board "X"; valid users: Ann`, resultText(t, res))
}

func TestErrorResultOther(t *testing.T) {
	res, _, _ := errorResult(errors.New("boom"))
	assert.True(t, res.IsError)
	assert.Equal(t, "boom", resultText(t, res))

	res, _, _ = errorResult(statusErr(500))
	assert.Equal(t, "planka: status 500", resultText(t, res))
}

func TestJSONResultIsCompact(t *testing.T) {
	res, out, err := jsonResult(map[string]any{"a": 1, "b": []string{"x"}})
	require.NoError(t, err)
	assert.Nil(t, out)
	assert.False(t, res.IsError)
	assert.JSONEq(t, `{"a":1,"b":["x"]}`, resultText(t, res))
}
