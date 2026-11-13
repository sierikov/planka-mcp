//go:build e2e

package e2e

import (
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFlowBoard builds a project with one board of the usual lists and returns it with an MCP
// the API client that built it.
func newFlowBoard(t *testing.T) (*planka, *board) {
	t.Helper()
	p := requirePlanka(t)
	id := unique(t)
	project := p.newProject(t, "e2e-"+id)
	b := p.newBoard(t, project, "Board "+id, "Open", "In progress", "Done")
	return p, b
}

// TestFlowFillInTicket is spec flow 1: find a card by words, fill in its description, a new label, a
// due date with a time, an assignee and a running stopwatch, then a checklist with a plain task and a
// linked card, and read it all back.
func TestFlowFillInTicket(t *testing.T) {
	p, b := newFlowBoard(t)
	me := p.meName(t)
	readme := p.newCard(t, b, "Open", "Write README")
	research := p.newCard(t, b, "In progress", "Research search API")
	s := connectStdio(t, env.key)

	found := callInto[searchResult](t, s, "search_cards", map[string]any{"query": "readme", "board": b.Name})
	require.Len(t, found.Cards, 1)
	assert.Equal(t, readme, found.Cards[0].ID)
	assert.Equal(t, "Open", found.Cards[0].List)

	card := callInto[cardDetail](t, s, "get_card", map[string]any{"card": "readme", "board": b.Name})
	assert.Equal(t, readme, card.ID)
	assert.Empty(t, card.Description)

	const description = "Document how to install and configure planka-mcp.\n\n- **install**\n- configure"
	// The server runs in serverZone, so "14:30" is 14:30 there.
	const due = "2026-12-01 14:30"
	updated := callInto[writeResult](t, s, "update_card", map[string]any{
		"card": "readme", "board": b.Name, "description": description, "add_labels": []string{"research2"}, "create_labels": true,
		"due": due, "stopwatch": "start", "add_assignees": []string{"me"},
	})
	assert.Equal(t, readme, updated.Card.ID)
	assert.Equal(t, []string{"research2"}, updated.CreatedLabels, "the label did not exist, so it is created")
	assert.Equal(t, due, updated.Card.Due)
	assert.Equal(t, []string{me}, updated.Card.Assignees)

	// Planka itself holds the instant and a started stopwatch.
	stored := p.card(t, readme)
	require.NotNil(t, stored.DueDate)
	assert.True(t, time.Date(2026, 12, 1, 14, 30, 0, 0, serverLoc).Equal(*stored.DueDate), "Planka stored %s", stored.DueDate)
	require.NotNil(t, stored.Stopwatch)
	require.NotNil(t, stored.Stopwatch.StartedAt, "the stopwatch runs")
	assert.WithinDuration(t, time.Now(), *stored.Stopwatch.StartedAt, time.Minute)

	list := callInto[taskList](t, s, "add_tasks", map[string]any{
		"card": "readme", "board": b.Name,
		"items": []map[string]any{{"name": "Draft the install section"}, {"link": research}},
	})
	assert.Equal(t, "Tasks", list.Name)
	require.Len(t, list.Tasks, 2)

	filled := callInto[cardDetail](t, s, "get_card", map[string]any{"card": readme})
	assert.Equal(t, description, filled.Description)
	assert.Equal(t, []string{"research2"}, filled.Labels)
	assert.Equal(t, due, filled.Due)
	assert.Equal(t, []string{me}, filled.Assignees)
	assert.Contains(t, filled.Stopwatch, "(running)")
	require.Len(t, filled.TaskLists, 1)
	require.Len(t, filled.TaskLists[0].Tasks, 2)
	assert.Equal(t, "Draft the install section", filled.TaskLists[0].Tasks[0].Name)
	linked := filled.TaskLists[0].Tasks[1]
	assert.Equal(t, "Research search API", linked.Name)
	require.NotNil(t, linked.Link, "the second task is a link to a card")
	assert.Equal(t, research, linked.Link.ID)
}

// TestFlowWorkOnTicket is spec flow 2: move a card to In progress, comment on it, move it to Done.
func TestFlowWorkOnTicket(t *testing.T) {
	p, b := newFlowBoard(t)
	card := p.newCard(t, b, "Open", "Fix the login bug")
	s := connectStdio(t, env.key)

	moved := callInto[writeResult](t, s, "move_card", map[string]any{"card": "login bug", "board": b.Name, "list": "In progress"})
	assert.Equal(t, card, moved.Card.ID)
	assert.Equal(t, "In progress", moved.Card.List)

	added := callInto[comment](t, s, "add_comment", map[string]any{"card": card, "text": "Reproduced; fix is in review."})
	assert.NotEmpty(t, added.ID)

	finished := callInto[writeResult](t, s, "move_card", map[string]any{"card": card, "list": "Done"})
	assert.Equal(t, "Done", finished.Card.List)

	got := callInto[boardDetail](t, s, "get_board", map[string]any{"board": b.Name})
	var titles = map[string][]string{}
	for _, l := range got.Lists {
		for _, c := range l.Cards {
			titles[l.Name] = append(titles[l.Name], c.Title)
		}
	}
	assert.Equal(t, []string{"Fix the login bug"}, titles["Done"])
	assert.Empty(t, titles["Open"])
	assert.Empty(t, titles["In progress"])

	detail := callInto[cardDetail](t, s, "get_card", map[string]any{"card": card})
	assert.Equal(t, "Done", detail.List)
	require.Len(t, detail.Comments, 1)
	assert.Equal(t, "Reproduced; fix is in review.", detail.Comments[0].Text)
}

// TestCreateCardChecklistAndComments creates a card with labels, a due date and an assignee, works
// its checklist (tick, delete) and its comments (edit, delete), and pages the comments that are left.
func TestCreateCardChecklistAndComments(t *testing.T) {
	p, b := newFlowBoard(t)
	me := p.meName(t)
	s := connectStdio(t, env.key)

	created := callInto[writeResult](t, s, "create_card", map[string]any{
		"board": b.Name, "list": "In progress", "title": "Wire up the backup script", "description": "Nightly, to the NAS.",
		"labels": []string{"ops", "ops"}, "create_labels": true, "assignees": []string{"me"}, "due": "2026-11-15", "position": "bottom",
	})
	card := created.Card.ID
	require.NotEmpty(t, card)
	assert.Equal(t, "Wire up the backup script", created.Card.Title)
	assert.Equal(t, "In progress", created.Card.List)
	assert.Equal(t, []string{"ops"}, created.Card.Labels)
	assert.Equal(t, []string{"ops"}, created.CreatedLabels, "a missing label is created once")
	assert.Equal(t, []string{me}, created.Card.Assignees)
	assert.Equal(t, "2026-11-15", created.Card.Due, "a date alone is midnight in the server's zone")
	stored := p.card(t, card)
	assert.Equal(t, b.lists["In progress"], stored.ListID)
	require.NotNil(t, stored.DueDate)
	assert.True(t, time.Date(2026, 11, 15, 0, 0, 0, 0, serverLoc).Equal(*stored.DueDate), "Planka stored %s", stored.DueDate)

	list := callInto[taskList](t, s, "add_tasks", map[string]any{
		"card": card, "items": []map[string]any{{"name": "Write the script"}, {"name": "Schedule it"}},
	})
	require.Len(t, list.Tasks, 2)
	write, schedule := list.Tasks[0], list.Tasks[1]
	assert.False(t, write.Done)

	ticked := callInto[[]task](t, s, "update_tasks", map[string]any{
		"items": []map[string]any{{"task_id": write.ID, "completed": true}},
	})
	require.Len(t, ticked, 1)
	assert.Equal(t, write.ID, ticked[0].ID)
	assert.True(t, ticked[0].Done)

	first := callInto[comment](t, s, "add_comment", map[string]any{"card": card, "text": "First draft is up."})
	second := callInto[comment](t, s, "add_comment", map[string]any{"card": card, "text": "Ignore this one."})
	require.NotEqual(t, first.ID, second.ID)

	edited := callInto[comment](t, s, "update_comment", map[string]any{"comment_id": first.ID, "text": "First draft is up; **review** it."})
	assert.Equal(t, first.ID, edited.ID)
	assert.Equal(t, "First draft is up; **review** it.", edited.Text)

	gone := callInto[deletedOut](t, s, "delete_comment", map[string]any{"comment_id": second.ID})
	assert.Equal(t, second.ID, gone.Deleted)

	goneTask := callInto[deletedOut](t, s, "delete_task", map[string]any{"task_id": schedule.ID})
	assert.Equal(t, schedule.ID, goneTask.Deleted)

	comments := callInto[commentsResult](t, s, "list_comments", map[string]any{"card": card})
	require.Len(t, comments.Comments, 1, "the deleted comment is gone")
	assert.False(t, comments.Older)
	assert.Equal(t, first.ID, comments.Comments[0].ID)
	assert.Equal(t, "First draft is up; **review** it.", comments.Comments[0].Text)

	detail := callInto[cardDetail](t, s, "get_card", map[string]any{"card": card})
	assert.Equal(t, "Nightly, to the NAS.", detail.Description)
	require.Len(t, detail.TaskLists, 1)
	require.Len(t, detail.TaskLists[0].Tasks, 1, "the deleted task is gone")
	assert.Equal(t, write.ID, detail.TaskLists[0].Tasks[0].ID)
	assert.True(t, detail.TaskLists[0].Tasks[0].Done)
	require.Len(t, detail.Comments, 1)
	assert.Equal(t, first.ID, detail.Comments[0].ID)

	cleared := callInto[writeResult](t, s, "update_card", map[string]any{"card": card, "due": nil})
	assert.Empty(t, cleared.Card.Due, "due: null clears the due date")
	assert.Nil(t, p.card(t, card).DueDate)
}

// TestMoveAcrossBoards moves a card to a list of another board in the same project with to_board.
func TestMoveAcrossBoards(t *testing.T) {
	p := requirePlanka(t)
	id := unique(t)
	project := p.newProject(t, "e2e-"+id)
	from := p.newBoard(t, project, "From "+id, "Open")
	to := p.newBoard(t, project, "To "+id, "Backlog", "Doing")
	card := p.newCard(t, from, "Open", "Move me across")
	s := connectStdio(t, env.key)

	moved := callInto[writeResult](t, s, "move_card", map[string]any{
		"card": "move me across", "board": from.Name, "list": "Doing", "to_board": to.Name,
	})
	assert.Equal(t, card, moved.Card.ID)
	assert.Equal(t, to.Name, moved.Card.Board)
	assert.Equal(t, "Doing", moved.Card.List)

	stored := p.card(t, card)
	assert.Equal(t, to.ID, stored.BoardID)
	assert.Equal(t, to.lists["Doing"], stored.ListID)

	target := callInto[boardDetail](t, s, "get_board", map[string]any{"board": to.ID, "list": "Doing"})
	require.Len(t, target.Lists, 1)
	require.Len(t, target.Lists[0].Cards, 1)
	assert.Equal(t, card, target.Lists[0].Cards[0].ID)
	source := callInto[boardDetail](t, s, "get_board", map[string]any{"board": from.ID})
	require.Len(t, source.Lists, 1)
	assert.Empty(t, source.Lists[0].Cards, "the card left its old board")
}

// TestDuplicateListNames: with two lists called "Done", a move by name is an error that gives both
// IDs, and a retry with one of them moves the card to that list.
func TestDuplicateListNames(t *testing.T) {
	p, b := newFlowBoard(t)
	second := p.newList(t, b, "Done", positionGap*10)
	card := p.newCard(t, b, "Open", "Pick a Done")
	s := connectStdio(t, env.key)

	text, isError := call(t, s, "move_card", map[string]any{"card": card, "list": "Done"})
	require.True(t, isError, text)
	assert.Contains(t, text, "Done (id "+b.lists["Done"]+")")
	assert.Contains(t, text, "Done (id "+second+")")
	assert.Equal(t, b.lists["Open"], p.card(t, card).ListID, "an ambiguous move moves nothing")

	moved := callInto[writeResult](t, s, "move_card", map[string]any{"card": card, "list": second})
	assert.Equal(t, "Done", moved.Card.List)
	assert.Equal(t, second, p.card(t, card).ListID)
}

// TestCustomFieldNames gives a card a value in a board's own group and one in a group made from the
// project's base group, whose name and fields live only on the project, and reads both back named.
func TestCustomFieldNames(t *testing.T) {
	p, b := newFlowBoard(t)
	card := p.newCard(t, b, "Open", "Backups")

	group := p.send(t, http.MethodPost, "/boards/"+b.ID+"/custom-field-groups", map[string]any{
		"position": positionGap, "name": "Release",
	}).Item.ID
	field := p.send(t, http.MethodPost, "/custom-field-groups/"+group+"/custom-fields", map[string]any{
		"position": positionGap, "name": "Version", "showOnFrontOfCard": true,
	}).Item.ID
	p.send(t, http.MethodPatch, "/cards/"+card+"/custom-field-values/customFieldGroupId:"+group+":customFieldId:"+field,
		map[string]any{"content": "1.2"})

	base := p.send(t, http.MethodPost, "/projects/"+b.ProjectID+"/base-custom-field-groups", map[string]any{
		"position": positionGap, "name": "Backup",
	}).Item.ID
	baseField := p.send(t, http.MethodPost, "/base-custom-field-groups/"+base+"/custom-fields", map[string]any{
		"position": positionGap, "name": "Tool",
	}).Item.ID
	cardGroup := p.send(t, http.MethodPost, "/cards/"+card+"/custom-field-groups", map[string]any{
		"position": positionGap, "baseCustomFieldGroupId": base,
	}).Item.ID
	p.send(t, http.MethodPatch, "/cards/"+card+"/custom-field-values/customFieldGroupId:"+cardGroup+":customFieldId:"+baseField,
		map[string]any{"content": "Zerobyte"})

	got := callInto[cardDetail](t, connectStdio(t, env.key), "get_card", map[string]any{"card": card})

	// The board's group comes before the card's own; each lists its fields.
	require.Len(t, got.CustomFields, 2)
	assert.Equal(t, "Release", got.CustomFields[0].Group)
	require.Len(t, got.CustomFields[0].Fields, 1)
	assert.Equal(t, "Version", got.CustomFields[0].Fields[0].Name)
	assert.Equal(t, "1.2", got.CustomFields[0].Fields[0].Value)
	assert.True(t, got.CustomFields[0].Fields[0].ShowOnFrontOfCard)
	assert.Equal(t, "Backup", got.CustomFields[1].Group)
	require.Len(t, got.CustomFields[1].Fields, 1)
	assert.Equal(t, "Tool", got.CustomFields[1].Fields[0].Name)
	assert.Equal(t, "Zerobyte", got.CustomFields[1].Fields[0].Value)
	assert.False(t, got.CustomFields[1].Fields[0].ShowOnFrontOfCard)
}

// TestUnknownLabelIsNotCreatedByAccident: a label name the board lacks is an error listing the
// board's labels, unless create_labels asks for it to be made.
func TestUnknownLabelIsNotCreatedByAccident(t *testing.T) {
	p, b := newFlowBoard(t)
	card := p.newCard(t, b, "Open", "Typo target")
	s := connectStdio(t, env.key)
	callInto[writeResult](t, s, "update_card", map[string]any{"card": card, "add_labels": []string{"feature"}, "create_labels": true})

	text, isError := call(t, s, "update_card", map[string]any{"card": card, "add_labels": []string{"featrue"}})
	assert.True(t, isError, text)
	assert.Contains(t, text, `no label "featrue"`)
	assert.Contains(t, text, "valid labels: feature")
	assert.Contains(t, text, "set create_labels to create it")

	got := callInto[cardDetail](t, s, "get_card", map[string]any{"card": card})
	assert.Equal(t, []string{"feature"}, got.Labels, "nothing was written")
}

// TestLinkTaskFollowsItsCard checks that a task linking a card cannot be ticked or renamed (Planka
// refuses), that the refusal explains itself, and that the task shows the linked card's current title
// after the card is renamed.
func TestLinkTaskFollowsItsCard(t *testing.T) {
	p, b := newFlowBoard(t)
	parent := p.newCard(t, b, "Open", "Parent")
	child := p.newCard(t, b, "Open", "Child before rename")
	s := connectStdio(t, env.key)

	list := callInto[taskList](t, s, "add_tasks", map[string]any{"card": parent, "items": []map[string]any{{"link": child}}})
	require.Len(t, list.Tasks, 1)
	linkTask := list.Tasks[0].ID

	text, isError := call(t, s, "update_tasks", map[string]any{"items": []map[string]any{{"task_id": linkTask, "completed": true}}})
	assert.True(t, isError, text)
	assert.Contains(t, text, "links a card", text)

	callInto[writeResult](t, s, "update_card", map[string]any{"card": child, "title": "Child after rename"})
	got := callInto[cardDetail](t, s, "get_card", map[string]any{"card": parent})
	require.Len(t, got.TaskLists, 1)
	assert.Equal(t, "Child after rename", got.TaskLists[0].Tasks[0].Name)
}

// TestArchiveAndDelete archives a card, checks searches see it only when asked, then deletes it for good.
// TestDeleteNeverPicksADescriptionMatch: a word found only in a card's
// description finds the card for a read, but delete_card refuses it, names the
// card's ID and deletes nothing.
func TestDeleteNeverPicksADescriptionMatch(t *testing.T) {
	p, b := newFlowBoard(t)
	card := p.newCard(t, b, "Open", "Quarterly report")
	s := connectStdio(t, env.key)
	callInto[writeResult](t, s, "update_card", map[string]any{"card": card, "description": "Due xqzvwk-rapidly."})

	read := callInto[cardDetail](t, s, "get_card", map[string]any{"card": "xqzvwk", "board": b.Name})
	assert.Equal(t, card, read.ID, "a read still finds the card by its description")

	text, isError := call(t, s, "delete_card", map[string]any{"card": "xqzvwk", "board": b.Name})
	assert.True(t, isError, text)
	assert.Contains(t, text, "matches only the description of card Quarterly report")
	assert.Contains(t, text, "(id "+card+")")

	still := callInto[cardDetail](t, s, "get_card", map[string]any{"card": card})
	assert.Equal(t, "Quarterly report", still.Title, "nothing was deleted")
}

func TestArchiveAndDelete(t *testing.T) {
	p, b := newFlowBoard(t)
	card := p.newCard(t, b, "Open", "Obsolete spike")
	s := connectStdio(t, env.key)

	archived := callInto[writeResult](t, s, "move_card", map[string]any{"card": card, "list": "archive"})
	assert.Equal(t, card, archived.Card.ID)
	assert.Equal(t, "(archived)", archived.Card.List)

	found := callInto[searchResult](t, s, "search_cards", map[string]any{"query": "obsolete spike", "board": b.Name})
	assert.Empty(t, found.Cards, "archived cards stay out of a plain search")

	withArchived := callInto[searchResult](t, s, "search_cards", map[string]any{"query": "obsolete spike", "board": b.Name, "include_archived": true})
	require.Len(t, withArchived.Cards, 1)
	assert.Equal(t, card, withArchived.Cards[0].ID)
	assert.Equal(t, "(archived)", withArchived.Cards[0].List)

	deleted := callInto[deletedOut](t, s, "delete_card", map[string]any{"card": card})
	assert.Equal(t, card, deleted.Deleted)
	assert.Equal(t, "Obsolete spike", deleted.Title)

	text, isError := call(t, s, "get_card", map[string]any{"card": card, "include_archived": true})
	assert.True(t, isError, text)
	assert.True(t, containsFold(text, "not found"), text)
}

// TestHTTPTwoUsers serves two Planka users from one HTTP server, each with their own key, and checks
// that each sees only the boards they belong to.
func TestHTTPTwoUsers(t *testing.T) {
	p := requirePlanka(t)
	id := unique(t)
	project := p.newProject(t, "e2e-"+id)
	shared := p.newBoard(t, project, "Shared "+id, "Open")
	private := p.newBoard(t, project, "Private "+id, "Open")
	userID, userKey := p.newUser(t, id)
	p.addMember(t, shared, userID)

	endpoint := startHTTPServer(t)

	admin := connectHTTP(t, endpoint, env.key)
	adminBoards := callInto[[]projectBoards](t, admin, "list_boards", map[string]any{"query": id})
	assert.ElementsMatch(t, []string{shared.ID, private.ID}, boardIDs(adminBoards))

	user := connectHTTP(t, endpoint, userKey)
	userBoards := callInto[[]projectBoards](t, user, "list_boards", map[string]any{})
	assert.Equal(t, []string{shared.ID}, boardIDs(userBoards), "the user is a member of one board only")

	text, isError := call(t, user, "get_board", map[string]any{"board": private.ID})
	assert.True(t, isError, "the user cannot open a board they are not on: %s", text)

	// A request without a key never reaches MCP.
	resp, err := http.Post(endpoint, "application/json", strings.NewReader("{}")) //nolint:noctx,gosec // plain probe of a local server
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// startHTTPServer runs the binary in HTTP mode on a free local port, with no Planka key of its own,
// and returns its MCP endpoint.
func startHTTPServer(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	cmd := exec.CommandContext(t.Context(), env.binary, "-transport", "http", "-addr", addr) //nolint:gosec // the binary is the one TestMain just built
	cmd.Env = serverEnv("PLANKA_URL=" + env.url)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Wait() })

	require.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/healthz", http.NoBody)
		if err != nil {
			return false
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 10*time.Second, 100*time.Millisecond, "the HTTP server did not come up on %s", addr)
	return "http://" + addr + "/mcp"
}
