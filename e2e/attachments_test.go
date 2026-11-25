//go:build e2e

package e2e

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// attachment is an attachment as the tools return it.
type attachment struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Size  int64  `json:"size"`
	URL   string `json:"url"`
	Cover bool   `json:"cover"`
}

// pngImage is a small valid PNG, which Planka reads as an image.
func pngImage(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))))
	return buf.Bytes()
}

// download reads an attachment's file from Planka as the admin.
func download(t *testing.T, url string) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("X-Api-Key", env.key)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return data
}

// TestAttachmentTools uploads an image and a text file and attaches a link on
// a real Planka, renames them, moves the cover and deletes one, reading the
// card back with get_card. Hidden files, folders and relative paths are
// refused.
func TestAttachmentTools(t *testing.T) {
	p, b := newFlowBoard(t)
	s := connectStdio(t, env.key)
	card := p.newCard(t, b, "Open", "Attach to me")
	dir := t.TempDir()
	photo := filepath.Join(dir, "photo.png")
	require.NoError(t, os.WriteFile(photo, pngImage(t), 0o600))
	notes := filepath.Join(dir, "Überweisung März.txt")
	require.NoError(t, os.WriteFile(notes, []byte("paid on time\n"), 0o600))

	img := callInto[attachment](t, s, "add_attachment", map[string]any{"card": card, "path": photo})
	assert.Equal(t, "photo.png", img.Name)
	assert.Equal(t, "file", img.Type)
	assert.Equal(t, int64(len(pngImage(t))), img.Size)
	assert.True(t, img.Cover, "Planka makes a card's first image its cover")
	assert.Equal(t, pngImage(t), download(t, img.URL), "the file arrives intact")

	txt := callInto[attachment](t, s, "add_attachment", map[string]any{"card": card, "path": notes})
	assert.Equal(t, "Überweisung März.txt", txt.Name)
	assert.False(t, txt.Cover)
	assert.Equal(t, "paid on time\n", string(download(t, txt.URL)))

	// A host that refuses at once: Planka looks for the link's favicon.
	link := callInto[attachment](t, s, "add_attachment", map[string]any{"card": card, "url": "http://127.0.0.1:1/wiki/docs", "name": "Docs"})
	assert.Equal(t, attachment{ID: link.ID, Name: "Docs", Type: "link", URL: "http://127.0.0.1:1/wiki/docs"}, link)

	got := callInto[struct {
		Attachments []attachment `json:"attachments"`
	}](t, s, "get_card", map[string]any{"card": card})
	require.Len(t, got.Attachments, 3)
	byName := map[string]attachment{}
	for _, a := range got.Attachments {
		byName[a.Name] = a
	}
	assert.True(t, byName["photo.png"].Cover)
	assert.Equal(t, txt.Size, byName[txt.Name].Size)
	assert.Equal(t, "link", byName["Docs"].Type)

	renamed := callInto[attachment](t, s, "update_attachment", map[string]any{"card": card, "attachment": txt.Name, "name": "Bank transfer"})
	assert.Equal(t, "Bank transfer", renamed.Name)
	uncovered := callInto[attachment](t, s, "update_attachment", map[string]any{"card": card, "attachment": "photo.png", "cover": false})
	assert.False(t, uncovered.Cover)
	assert.Nil(t, p.card(t, card).CoverAttachmentID)
	text, isError := call(t, s, "update_attachment", map[string]any{"card": card, "attachment": "Bank transfer", "cover": true})
	assert.True(t, isError, text)
	assert.Contains(t, text, "only an image can be the card's cover")
	callInto[attachment](t, s, "update_attachment", map[string]any{"card": card, "attachment": img.ID, "cover": true})
	require.NotNil(t, p.card(t, card).CoverAttachmentID)
	assert.Equal(t, img.ID, *p.card(t, card).CoverAttachmentID)

	deleted := callInto[struct {
		Deleted, Name, Type, URL string
	}](t, s, "delete_attachment", map[string]any{"card": card, "attachment": "photo.png"})
	assert.Equal(t, img.ID, deleted.Deleted)
	assert.Empty(t, deleted.URL, "a deleted file's address is not returned")
	assert.Nil(t, p.card(t, card).CoverAttachmentID, "Planka drops the cover with its attachment")
	deletedLink := callInto[struct{ URL string }](t, s, "delete_attachment", map[string]any{"card": card, "attachment": "docs"})
	assert.Equal(t, "http://127.0.0.1:1/wiki/docs", deletedLink.URL)

	hiddenDir := filepath.Join(dir, ".config")
	require.NoError(t, os.Mkdir(hiddenDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(hiddenDir, "token"), []byte("secret"), 0o600))
	for path, want := range map[string]string{
		filepath.Join(hiddenDir, "token"): "hidden file or folder (.config)",
		dir:                               "is a folder",
		"photo.png":                       "is not absolute",
	} {
		text, isError := call(t, s, "add_attachment", map[string]any{"card": card, "path": path})
		assert.True(t, isError, text)
		assert.Contains(t, text, want)
	}
	got = callInto[struct {
		Attachments []attachment `json:"attachments"`
	}](t, s, "get_card", map[string]any{"card": card})
	assert.Len(t, got.Attachments, 1, "only the text file is left")
}

// TestHTTPModeAttachesLinksOnly: the shared server runs on another machine, so
// add_attachment refuses a path there and still attaches a link.
func TestHTTPModeAttachesLinksOnly(t *testing.T) {
	p, b := newFlowBoard(t)
	card := p.newCard(t, b, "Open", "Remote attach")
	s := connectHTTP(t, startHTTPServer(t), env.key)

	text, isError := call(t, s, "add_attachment", map[string]any{"card": card, "path": "/etc/hosts"})
	assert.True(t, isError, text)
	assert.Contains(t, text, `"path"`)

	link := callInto[attachment](t, s, "add_attachment", map[string]any{"card": card, "url": "http://127.0.0.1:1/remote"})
	assert.Equal(t, "link", link.Type)
}
