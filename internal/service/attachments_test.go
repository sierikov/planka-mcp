package service

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka"
)

type attachmentsFixture struct {
	*cardsFixture
	api         *MockAttachmentsAPI
	attachments *Attachments
}

func newAttachmentsFixture(t *testing.T) *attachmentsFixture {
	t.Helper()
	f := &attachmentsFixture{cardsFixture: newCardsFixture(t), api: NewMockAttachmentsAPI(t)}
	f.attachments = NewAttachments(f.svc, f.api, true)
	return f
}

// withAttachments is card c1 with the given attachments; cover is its cover ID.
func withAttachments(cover string, all ...planka.Attachment) *planka.CardResponse {
	r := cardOf(nil)
	if cover != "" {
		r.Item.CoverAttachmentID = &cover
	}
	r.Included.Attachments = all
	return r
}

func fileAttachment(id, name string, image bool) planka.Attachment {
	a := planka.Attachment{ID: id, CardID: "c1", Name: name, Type: planka.AttachmentFile,
		Data: planka.AttachmentData{URL: "https://planka.example.com/attachments/" + id + "/download/" + name, Size: "100"}}
	if image {
		a.Data.Image = &planka.AttachmentImage{Width: 10, Height: 10}
	}
	return a
}

func linkAttachment(id, name, url string) planka.Attachment {
	return planka.Attachment{ID: id, CardID: "c1", Name: name, Type: planka.AttachmentLink, Data: planka.AttachmentData{URL: url}}
}

// writeFile writes data to name in a new temporary folder and returns its path.
func writeFile(t *testing.T, name, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
	return path
}

func TestAddFileUploadsItUnderItsBaseName(t *testing.T) {
	f := newAttachmentsFixture(t)
	path := writeFile(t, "scan.pdf", "%PDF-1.7")
	f.serve(writeBoard())
	f.api.EXPECT().CreateFileAttachment(mock.Anything, "c1", planka.FileUpload{
		Name: "scan.pdf", Filename: "scan.pdf", ContentType: "application/pdf", Data: []byte("%PDF-1.7"),
	}).Return(new(fileAttachment("a1", "scan.pdf", false)), nil).Once()

	got, err := f.attachments.Add(t.Context(), AddAttachment{Card: alphaRef, Path: " " + path + " "})

	require.NoError(t, err)
	assert.Equal(t, &Attachment{ID: "a1", Name: "scan.pdf", Type: "file", URL: "https://planka.example.com/attachments/a1/download/scan.pdf", Size: 100}, got)
}

func TestAddImageReportsWhetherItBecameTheCover(t *testing.T) {
	for name, cover := range map[string]string{"became the cover": "a1", "card had a cover": "a0"} {
		t.Run(name, func(t *testing.T) {
			f := newAttachmentsFixture(t)
			path := writeFile(t, "photo.png", "PNG")
			f.serve(writeBoard())
			f.api.EXPECT().CreateFileAttachment(mock.Anything, "c1", mock.Anything).
				Return(new(fileAttachment("a1", "Photo", true)), nil).Once()
			f.cards.EXPECT().Card(mock.Anything, "c1").Return(withAttachments(cover), nil).Once()

			got, err := f.attachments.Add(t.Context(), AddAttachment{Card: alphaRef, Path: path, Name: "Photo"})

			require.NoError(t, err)
			assert.Equal(t, cover == "a1", got.Cover)
		})
	}
}

func TestAddImageSucceedsWhenTheCoverCannotBeRead(t *testing.T) {
	f := newAttachmentsFixture(t)
	path := writeFile(t, "photo.png", "PNG")
	f.serve(writeBoard())
	f.api.EXPECT().CreateFileAttachment(mock.Anything, "c1", mock.Anything).Return(new(fileAttachment("a1", "photo.png", true)), nil).Once()
	f.cards.EXPECT().Card(mock.Anything, "c1").Return(nil, &planka.APIError{Status: http.StatusBadGateway}).Once()

	got, err := f.attachments.Add(t.Context(), AddAttachment{Card: alphaRef, Path: path})

	require.NoError(t, err, "the file is attached, so the call succeeds")
	assert.False(t, got.Cover)
}

func TestAddLink(t *testing.T) {
	f := newAttachmentsFixture(t)
	f.serve(writeBoard())
	f.api.EXPECT().CreateLinkAttachment(mock.Anything, "c1", "https://example.com/docs", "https://example.com/docs").
		Return(new(linkAttachment("a2", "https://example.com/docs", "https://example.com/docs")), nil).Once()

	got, err := f.attachments.Add(t.Context(), AddAttachment{Card: alphaRef, URL: "https://example.com/docs"})

	require.NoError(t, err)
	assert.Equal(t, &Attachment{ID: "a2", Name: "https://example.com/docs", Type: "link", URL: "https://example.com/docs"}, got)
}

func TestALongLinkIsNamedByItsHost(t *testing.T) {
	f := newAttachmentsFixture(t)
	link := "https://example.com/" + strings.Repeat("x", 200)
	f.serve(writeBoard())
	f.api.EXPECT().CreateLinkAttachment(mock.Anything, "c1", "example.com", link).
		Return(new(linkAttachment("a2", "example.com", link)), nil).Once()

	_, err := f.attachments.Add(t.Context(), AddAttachment{Card: alphaRef, URL: link})

	require.NoError(t, err)
}

func TestAddLinkWithoutUploadsStillWorks(t *testing.T) {
	f := newAttachmentsFixture(t)
	f.attachments = NewAttachments(f.svc, f.api, false)
	f.serve(writeBoard())
	f.api.EXPECT().CreateLinkAttachment(mock.Anything, "c1", "Docs", "http://wiki.local/docs").
		Return(new(linkAttachment("a2", "Docs", "http://wiki.local/docs")), nil).Once()

	_, err := f.attachments.Add(t.Context(), AddAttachment{Card: alphaRef, URL: "http://wiki.local/docs", Name: "Docs"})

	require.NoError(t, err)
}

// TestAddRefusesBeforeReadingTheBoard: each case is refused, and no read or
// write is expected, so none may happen.
func TestAddRefusesBeforeReadingTheBoard(t *testing.T) {
	dir := t.TempDir()
	hiddenDir := filepath.Join(dir, ".secret")
	require.NoError(t, os.Mkdir(hiddenDir, 0o700))
	hidden := filepath.Join(hiddenDir, "key.txt")
	require.NoError(t, os.WriteFile(hidden, []byte("k"), 0o600))
	link := filepath.Join(dir, "innocent.txt")
	require.NoError(t, os.Symlink(hidden, link))
	dotFile := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(dotFile, []byte("A=1"), 0o600))
	big := filepath.Join(dir, "big.bin")
	require.NoError(t, os.WriteFile(big, nil, 0o600))
	require.NoError(t, os.Truncate(big, maxUpload+1))
	longName := writeFile(t, strings.Repeat("n", 130)+".txt", "x")

	for name, tc := range map[string]struct {
		in      AddAttachment
		uploads bool
		want    string
	}{
		"neither":             {AddAttachment{Card: alphaRef}, true, "give path (a file on this computer) or url"},
		"both":                {AddAttachment{Card: alphaRef, Path: big, URL: "https://x.example"}, true, "not both"},
		"uploads off":         {AddAttachment{Card: alphaRef, Path: big}, false, "attach a link with url instead"},
		"relative path":       {AddAttachment{Card: alphaRef, Path: "docs/scan.pdf"}, true, "is not absolute"},
		"hidden file":         {AddAttachment{Card: alphaRef, Path: dotFile}, true, "hidden file or folder (.env)"},
		"hidden folder":       {AddAttachment{Card: alphaRef, Path: hidden}, true, "hidden file or folder (.secret)"},
		"symlink to hidden":   {AddAttachment{Card: alphaRef, Path: link}, true, "hidden file or folder (.secret)"},
		"dot-dot into hidden": {AddAttachment{Card: alphaRef, Path: hiddenDir + "/../.secret/key.txt"}, true, "hidden"},
		"folder":              {AddAttachment{Card: alphaRef, Path: dir}, true, "is a folder"},
		"missing":             {AddAttachment{Card: alphaRef, Path: filepath.Join(dir, "nope.pdf")}, true, "there is no file at"},
		"too large":           {AddAttachment{Card: alphaRef, Path: big}, true, "larger than 50 MB"},
		"long default name":   {AddAttachment{Card: alphaRef, Path: longName}, true, "give a shorter name"},
		"long name":           {AddAttachment{Card: alphaRef, URL: "https://x.example", Name: strings.Repeat("n", 129)}, true, "longer than 128"},
		"not http":            {AddAttachment{Card: alphaRef, URL: "file:///etc/passwd"}, true, "not an http or https link"},
		"no host":             {AddAttachment{Card: alphaRef, URL: "https:///path"}, true, "not an http or https link"},
		"long url":            {AddAttachment{Card: alphaRef, URL: "https://x.example/" + strings.Repeat("u", 2048)}, true, "longer than 2048"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newAttachmentsFixture(t)
			f.attachments = NewAttachments(f.svc, f.api, tc.uploads)

			_, err := f.attachments.Add(t.Context(), tc.in)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestAbsPathExpandsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	got, err := absPath("~/Documents/../scan.pdf")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "scan.pdf"), got)
}

// lockedAttachments expects the reads of an attachment write on c1: the board
// found by title, the board again under the lock, then the card.
func (f *attachmentsFixture) lockedAttachments(card *planka.CardResponse) {
	f.serveLocked(writeBoard())
	f.cards.EXPECT().Card(mock.Anything, "c1").Return(card, nil).Once()
}

func TestUpdateAttachmentRenames(t *testing.T) {
	f := newAttachmentsFixture(t)
	f.lockedAttachments(withAttachments("5001", fileAttachment("5001", "scan.pdf", true)))
	renamed := fileAttachment("5001", "Bescheid", true)
	f.api.EXPECT().UpdateAttachment(mock.Anything, "5001", "Bescheid").Return(&renamed, nil).Once()

	got, err := f.attachments.Update(t.Context(), UpdateAttachment{Card: alphaRef, Attachment: "SCAN.pdf", Name: new(" Bescheid ")})

	require.NoError(t, err)
	assert.Equal(t, "Bescheid", got.Name)
	assert.True(t, got.Cover, "the cover is unchanged")
}

func TestUpdateAttachmentCover(t *testing.T) {
	for name, tc := range map[string]struct {
		cover string
		want  bool
		patch *planka.Optional[string] // nil: no card update
	}{
		"make it the cover":         {cover: "", want: true, patch: new(planka.Some("5001"))},
		"replace another cover":     {cover: "5002", want: true, patch: new(planka.Some("5001"))},
		"already the cover":         {cover: "5001", want: true},
		"remove the cover":          {cover: "5001", want: false, patch: new(planka.Null[string]())},
		"another one is the cover":  {cover: "5002", want: false},
		"no cover, none is removed": {cover: "", want: false},
	} {
		t.Run(name, func(t *testing.T) {
			f := newAttachmentsFixture(t)
			f.lockedAttachments(withAttachments(tc.cover, fileAttachment("5001", "photo.png", true), fileAttachment("5002", "other.png", true)))
			if tc.patch != nil {
				f.updated(planka.UpdateCardRequest{CoverAttachmentID: *tc.patch})
			}

			got, err := f.attachments.Update(t.Context(), UpdateAttachment{Card: alphaRef, Attachment: "5001", Cover: new(tc.want)})

			require.NoError(t, err)
			assert.Equal(t, tc.want, got.Cover)
		})
	}
}

func TestUpdateAttachmentSaysARenameHappenedWhenTheCoverFails(t *testing.T) {
	f := newAttachmentsFixture(t)
	f.lockedAttachments(withAttachments("", fileAttachment("5001", "photo.png", true)))
	renamed := fileAttachment("5001", "Front", true)
	f.api.EXPECT().UpdateAttachment(mock.Anything, "5001", "Front").Return(&renamed, nil).Once()
	f.write.EXPECT().UpdateCard(mock.Anything, "c1", mock.Anything).Return(nil, &planka.APIError{Status: http.StatusBadGateway}).Once()

	_, err := f.attachments.Update(t.Context(), UpdateAttachment{Card: alphaRef, Attachment: "photo.png", Name: new("Front"), Cover: new(true)})

	require.Error(t, err)
	assert.Contains(t, err.Error(), `attachment renamed to "Front", but changing the cover failed`)
}

// TestUpdateAttachmentRefusesBeforeWriting: no write is expected, so none may
// happen.
func TestUpdateAttachmentRefusesBeforeWriting(t *testing.T) {
	card := withAttachments("", fileAttachment("5001", "notes.txt", false), linkAttachment("5002", "Docs", "https://example.com"),
		fileAttachment("5003", "Copy", false), fileAttachment("5004", "copy", false))
	for name, tc := range map[string]struct {
		in   UpdateAttachment
		read bool
		want string
	}{
		"nothing":        {UpdateAttachment{Card: alphaRef, Attachment: "notes.txt"}, false, "nothing to change"},
		"empty name":     {UpdateAttachment{Card: alphaRef, Attachment: "notes.txt", Name: new(" ")}, false, "name is empty"},
		"no attachment":  {UpdateAttachment{Card: alphaRef, Attachment: " ", Name: new("x")}, true, "attachment is empty"},
		"unknown":        {UpdateAttachment{Card: alphaRef, Attachment: "note", Name: new("x")}, true, `no attachment "note"`},
		"ambiguous":      {UpdateAttachment{Card: alphaRef, Attachment: "COPY", Name: new("x")}, true, "Copy (id 5003); copy (id 5004)"},
		"file not image": {UpdateAttachment{Card: alphaRef, Attachment: "notes.txt", Name: new("x"), Cover: new(true)}, true, "only an image can be the card's cover"},
		"link as cover":  {UpdateAttachment{Card: alphaRef, Attachment: "Docs", Cover: new(true)}, true, "only an image"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newAttachmentsFixture(t)
			if tc.read {
				f.lockedAttachments(card)
			}

			_, err := f.attachments.Update(t.Context(), tc.in)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestDeleteAttachmentReturnsItAsItWas(t *testing.T) {
	f := newAttachmentsFixture(t)
	f.lockedAttachments(withAttachments("5001", fileAttachment("5001", "photo.png", true)))
	f.api.EXPECT().DeleteAttachment(mock.Anything, "5001").Return(nil).Once()

	got, err := f.attachments.Delete(t.Context(), alphaRef, "photo.png")

	require.NoError(t, err)
	assert.Equal(t, &Attachment{ID: "5001", Name: "photo.png", Type: "file",
		URL: "https://planka.example.com/attachments/5001/download/photo.png", Size: 100, Cover: true}, got)
}

func TestDeleteAttachmentOfAnotherCardIsNotFound(t *testing.T) {
	f := newAttachmentsFixture(t)
	f.lockedAttachments(withAttachments("", fileAttachment("5001", "photo.png", true)))
	f.api.EXPECT().DeleteAttachment(mock.Anything, "5001").Return(&planka.APIError{Status: http.StatusNotFound}).Once()

	_, err := f.attachments.Delete(t.Context(), alphaRef, "5001")

	require.ErrorIs(t, err, ErrNotFound)
}
