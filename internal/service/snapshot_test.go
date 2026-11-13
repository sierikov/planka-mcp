package service

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka"
	"github.com/sierikov/planka-mcp/internal/planka/plankatest"
)

func fixtureSnapshot(t *testing.T) *boardSnapshot {
	t.Helper()
	var resp planka.BoardResponse
	require.NoError(t, json.Unmarshal(plankatest.Fixture(t, "board"), &resp))
	return newSnapshot(&resp, "Projects")
}

func TestSnapshotSummary(t *testing.T) {
	snap := fixtureSnapshot(t)

	got := snap.summary("1887856285739648016")
	assert.Equal(t, "Write README", got.Title)
	assert.Equal(t, "Planka MCP", got.Board)
	assert.Equal(t, "Open", got.List)
	assert.Equal(t, []string{"feature"}, got.Labels)
	assert.Equal(t, []string{"Admin"}, got.Assignees)
	assert.Equal(t, 1, got.TasksDone)
	assert.Equal(t, 3, got.TasksTotal)
	require.NotNil(t, got.Due)
	assert.Equal(t, "2026-10-24T12:00:00Z", got.Due.UTC().Format("2006-01-02T15:04:05Z07:00"))
	assert.False(t, got.DueCompleted)

	other := snap.summary("1887856285890642962")
	assert.Equal(t, "In progress", other.List)
	assert.Empty(t, other.Labels)
	assert.Nil(t, other.Due)
	assert.Zero(t, other.TasksTotal)
}

func TestSnapshotKanbanLists(t *testing.T) {
	snap := fixtureSnapshot(t)

	kanban := snap.kanbanLists()
	names := make([]string, 0, len(kanban))
	for _, l := range kanban {
		names = append(names, *l.Name)
	}
	assert.Equal(t, []string{"Open", "In progress", "Ready for release", "Done", "Dropped"}, names)
}

func TestSnapshotListByNameArchive(t *testing.T) {
	snap := fixtureSnapshot(t)

	archive, err := snap.listByName(" Archive ")
	require.NoError(t, err)
	assert.Equal(t, "archive", archive.Type)
	assert.Equal(t, "1887856284598797319", archive.ID)
	require.NotNil(t, snap.archiveList())
	assert.Equal(t, archive.ID, snap.archiveList().ID)

	done, err := snap.listByName("done")
	require.NoError(t, err)
	assert.Equal(t, "Done", *done.Name)

	_, err = snap.listByName("Doing")
	var unk *UnknownNameError
	require.ErrorAs(t, err, &unk)
	assert.Equal(t, []string{"Open", "In progress", "Ready for release", "Done", "Dropped"}, unk.Valid)
	assert.Equal(t,
		`no list "Doing" on board "Planka MCP"; lists: Open, In progress, Ready for release, Done, Dropped`,
		err.Error())
}

// TestSnapshotLookupsAcceptIDs: lists, labels and people can be given by ID as well as by name.
func TestSnapshotLookupsAcceptIDs(t *testing.T) {
	snap := fixtureSnapshot(t)

	done, err := snap.kanbanListByName(" 1887856285144056844 ")
	require.NoError(t, err)
	assert.Equal(t, "Done", *done.Name)

	moveTo, err := snap.listByName("1887856285144056844")
	require.NoError(t, err)
	assert.Equal(t, "1887856285144056844", moveTo.ID)

	label, err := snap.labelByName("1887856285521544207")
	require.NoError(t, err)
	assert.Equal(t, "research", *label.Name)

	user, err := snap.userByName("1887856237849084929", "x")
	require.NoError(t, err)
	assert.Equal(t, "Admin", user.Name)

	// An ID that is not on this board is an unknown name, which lists the valid names.
	_, err = snap.kanbanListByName("123")
	var unk *UnknownNameError
	require.ErrorAs(t, err, &unk)
	assert.Equal(t, "123", unk.Name)
}

// TestSnapshotIDBeatsName: a numeric value that is an ID on the board means that record, even when
// another record is called that.
func TestSnapshotIDBeatsName(t *testing.T) {
	snap := fixtureSnapshot(t)
	name := "1887856284758180873" // the ID of the list "Open"
	snap.resp.Included.Lists = append(snap.resp.Included.Lists,
		planka.List{ID: "999", BoardID: snap.resp.Item.ID, Type: "active", Name: &name, Position: new(500000.0)})
	snap = newSnapshot(snap.resp, "Projects")

	l, err := snap.kanbanListByName(name)
	require.NoError(t, err)
	assert.Equal(t, "Open", *l.Name)
}

// TestSnapshotAmbiguousNamesCarryIDs: two lists, labels or people with the same name are an error
// whose candidates carry the IDs to retry with.
func TestSnapshotAmbiguousNamesCarryIDs(t *testing.T) {
	snap := fixtureSnapshot(t)
	done, feature := "Done", "feature"
	snap.resp.Included.Lists = append(snap.resp.Included.Lists,
		planka.List{ID: "901", BoardID: snap.resp.Item.ID, Type: "closed", Name: &done, Position: new(1.0)})
	snap.resp.Included.Labels = append(snap.resp.Included.Labels,
		planka.Label{ID: "902", BoardID: snap.resp.Item.ID, Name: &feature, Position: 500000})
	snap.resp.Included.Users = append(snap.resp.Included.Users,
		planka.User{ID: "903", Name: "Sam", Username: "sam1"}, planka.User{ID: "904", Name: "Sam", Username: "sam2"})
	snap = newSnapshot(snap.resp, "Projects")

	for _, tc := range []struct {
		kind string
		err  error
		want []Candidate
	}{
		{"list", second(snap.kanbanListByName("done")), []Candidate{{ID: "1887856285144056844", Label: "Done"}, {ID: "901", Label: "Done"}}},
		{"label", second(snap.labelByName("feature")), []Candidate{{ID: "1887856285387326478", Label: "feature"}, {ID: "902", Label: "feature"}}},
		{"user", second(snap.userByName("sam", "x")), []Candidate{{ID: "903", Label: "Sam"}, {ID: "904", Label: "Sam"}}},
	} {
		var amb *AmbiguousError
		require.ErrorAs(t, tc.err, &amb, tc.kind)
		assert.Equal(t, tc.kind, amb.Kind)
		assert.Equal(t, tc.want, amb.Candidates, tc.kind)
	}
}

// second returns the error of a two-value call.
func second[T any](_ T, err error) error { return err }

func TestSnapshotListCalledArchiveBeatsReservedWord(t *testing.T) {
	snap := fixtureSnapshot(t)
	archive, archived := "Archive", "Archived ideas"
	base := snap.resp.Included.Lists

	snap.resp.Included.Lists = append(slices.Clone(base),
		planka.List{ID: "801", BoardID: snap.resp.Item.ID, Type: "active", Name: &archive, Position: new(400000.0)})
	got, err := newSnapshot(snap.resp, "Projects").listByName("ARCHIVE")
	require.NoError(t, err)
	assert.Equal(t, "801", got.ID, "an exact kanban list name wins")

	// A list whose name only starts with the word does not stop it meaning the archive.
	snap.resp.Included.Lists = append(slices.Clone(base),
		planka.List{ID: "802", BoardID: snap.resp.Item.ID, Type: "active", Name: &archived, Position: new(400000.0)})
	got, err = newSnapshot(snap.resp, "Projects").listByName("archive")
	require.NoError(t, err)
	assert.Equal(t, "archive", got.Type)
}

func TestSnapshotLabelByName(t *testing.T) {
	snap := fixtureSnapshot(t)

	l, err := snap.labelByName("FEAT")
	require.NoError(t, err)
	assert.Equal(t, "1887856285387326478", l.ID)

	_, err = snap.labelByName("bug")
	var unk *UnknownNameError
	require.ErrorAs(t, err, &unk)
	assert.Equal(t, []string{"feature", "research"}, unk.Valid)
}

func TestSnapshotColourOnlyLabel(t *testing.T) {
	snap := fixtureSnapshot(t)
	snap.resp.Included.Labels = append(snap.resp.Included.Labels,
		planka.Label{ID: "l3", Name: nil, Color: "berry-red", Position: 200000})
	snap = newSnapshot(snap.resp, "Projects")

	l, err := snap.labelByName("berry-red")
	require.NoError(t, err)
	assert.Equal(t, "l3", l.ID)
	assert.Equal(t, []string{"feature", "research", "berry-red"}, snap.board().Labels)
}

func TestSnapshotUserByName(t *testing.T) {
	snap := fixtureSnapshot(t)
	const meID = "1887856237849084929"

	byUsername, err := snap.userByName("ADMIN", "x")
	require.NoError(t, err)
	assert.Equal(t, meID, byUsername.ID)

	byName, err := snap.userByName("Adm", "x")
	require.NoError(t, err)
	assert.Equal(t, meID, byName.ID)

	me, err := snap.userByName("me", meID)
	require.NoError(t, err)
	assert.Equal(t, meID, me.ID)

	_, err = snap.userByName("nobody", meID)
	var unk *UnknownNameError
	require.ErrorAs(t, err, &unk)
	assert.Equal(t, []string{"Admin"}, unk.Valid)
}

func TestSnapshotUsernameBeatsFullName(t *testing.T) {
	snap := fixtureSnapshot(t)
	snap.resp.Included.Users = []planka.User{
		{ID: "u1", Name: "bob", Username: "robert"},
		{ID: "u2", Name: "Bob Two", Username: "bob"},
	}
	snap = newSnapshot(snap.resp, "Projects")

	u, err := snap.userByName("bob", "me")
	require.NoError(t, err)
	assert.Equal(t, "u2", u.ID, "an exact username match is tried first")
}

func TestSnapshotBoardMembers(t *testing.T) {
	snap := fixtureSnapshot(t)
	b := snap.board()
	assert.Equal(t, []string{"Admin"}, b.Members)
	assert.Equal(t, []string{"feature", "research"}, b.Labels)
	assert.Equal(t, "Projects", b.Project)
}

func TestSnapshotListNameFallback(t *testing.T) {
	snap := fixtureSnapshot(t)
	snap.resp.Included.Cards[0].ListID = "1887856284598797319" // the archive list
	snap = newSnapshot(snap.resp, "Projects")

	assert.Equal(t, "archive", snap.summary("1887856285739648016").List)
	assert.Equal(t, "Write README", snap.summary("1887856285739648016").Title)
	assert.Equal(t, CardSummary{ID: "missing"}, snap.summary("missing"))
}
