package planka

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka/plankatest"
)

// attachmentJSON is a file attachment as Planka 2.2.1 answers it.
const attachmentJSON = `{"item":{"id":"a1","cardId":"c1","type":"file","name":"Scan","data":{
	"mimeType":"image/png","size":2048,"encoding":null,"image":{"width":640,"height":480},
	"url":"https://planka.example.com/attachments/a1/download/scan.png"}}}`

// formPart is one part of a multipart form.
type formPart struct {
	name, filename, contentType, data string
}

// readForm splits a recorded multipart body into its parts, in order.
func readForm(t *testing.T, r plankatest.Recorded) []formPart {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(r.ContentType)
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	mr := multipart.NewReader(bytes.NewReader(r.Body), params["boundary"])
	var parts []formPart
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return parts
		}
		require.NoError(t, err)
		data, err := io.ReadAll(p)
		require.NoError(t, err)
		parts = append(parts, formPart{p.FormName(), p.FileName(), p.Header.Get("Content-Type"), string(data)})
	}
}

func TestCreateFileAttachmentSendsTheFieldsBeforeTheFile(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"POST /api/cards/c1/attachments": json.RawMessage(attachmentJSON)})

	a, err := c.CreateFileAttachment(t.Context(), "c1", FileUpload{
		Name: "Scan", Filename: "scan \"1\".png", ContentType: "image/png", Data: []byte("PNG-bytes"),
	})

	require.NoError(t, err)
	assert.Equal(t, "a1", a.ID)
	assert.Equal(t, AttachmentFile, a.Type)
	assert.Equal(t, "https://planka.example.com/attachments/a1/download/scan.png", a.Data.URL)
	size, err := a.Data.Size.Int64()
	require.NoError(t, err)
	assert.Equal(t, int64(2048), size)
	assert.Equal(t, &AttachmentImage{Width: 640, Height: 480}, a.Data.Image)
	assert.Equal(t, http.MethodPost, srv.Last().Method)
	assert.Equal(t, []formPart{
		{name: "type", data: "file"},
		{name: "name", data: "Scan"},
		{name: "file", filename: "scan \"1\".png", contentType: "image/png", data: "PNG-bytes"},
	}, readForm(t, srv.Last()))
}

func TestCreateFileAttachmentWithoutContentTypeSendsOctetStream(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"POST /api/cards/c1/attachments": json.RawMessage(attachmentJSON)})

	_, err := c.CreateFileAttachment(t.Context(), "c1", FileUpload{Name: "x", Filename: "x", Data: []byte{0}})

	require.NoError(t, err)
	assert.Equal(t, "application/octet-stream", readForm(t, srv.Last())[2].contentType)
}

func TestUploadIsRetriedWithItsBodyAfterRelogin(t *testing.T) {
	srv := loginServer(t, map[string]any{"POST /api/cards/1/attachments": json.RawMessage(attachmentJSON)})
	c := NewClient(srv.URL, WithLogin("ann", "pw"))
	_, err := c.Me(t.Context())
	require.NoError(t, err)
	srv.ExpireSessions()

	_, err = c.CreateFileAttachment(t.Context(), "1", FileUpload{Name: "Scan", Filename: "scan.png", Data: []byte("PNG-bytes")})

	require.NoError(t, err)
	last := srv.Last()
	assert.Equal(t, "POST /api/cards/1/attachments", last.Method+" "+last.Path)
	assert.Equal(t, "session-2", last.Bearer)
	assert.Equal(t, "PNG-bytes", readForm(t, last)[2].data, "the file is sent again in full")
}

func TestUploadsGetALongerTimeLimit(t *testing.T) {
	c := NewClient("http://planka.example.com")
	assert.Equal(t, 15*time.Second, c.httpClient.Timeout)
	assert.Equal(t, uploadTimeout, c.uploadClient.Timeout)
	assert.NotNil(t, c.uploadClient.CheckRedirect, "uploads do not follow redirects either")

	none := NewClient("http://planka.example.com", WithHTTPClient(&http.Client{}))
	assert.Zero(t, none.uploadClient.Timeout, "no time limit stays no time limit")
}

func TestCreateLinkAttachment(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"POST /api/cards/c1/attachments": json.RawMessage(`{"item":{"id":"a2","cardId":"c1","type":"link","name":"Docs","data":{"url":"https://example.com/docs","faviconUrl":"x"}}}`)})

	a, err := c.CreateLinkAttachment(t.Context(), "c1", "Docs", "https://example.com/docs")

	require.NoError(t, err)
	assert.Equal(t, AttachmentLink, a.Type)
	assert.Equal(t, "https://example.com/docs", a.Data.URL)
	assert.Equal(t, "application/json", srv.Last().ContentType)
	assert.JSONEq(t, `{"type":"link","name":"Docs","url":"https://example.com/docs"}`, string(srv.Last().Body))
}

func TestUpdateAndDeleteAttachment(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{
		"PATCH /api/attachments/a1":  json.RawMessage(attachmentJSON),
		"DELETE /api/attachments/a1": json.RawMessage(attachmentJSON),
	})

	_, err := c.UpdateAttachment(t.Context(), "a1", "Renamed")
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"Renamed"}`, string(srv.Last().Body))

	require.NoError(t, c.DeleteAttachment(t.Context(), "a1"))
	assert.Equal(t, "DELETE /api/attachments/a1", srv.Last().Method+" "+srv.Last().Path)
}

func TestUpdateCardSetsAndClearsTheCover(t *testing.T) {
	c, srv := newTestClient(t, map[string]any{"PATCH /api/cards/c1": plankatest.Fixture(t, "card_updated")})

	_, err := c.UpdateCard(t.Context(), "c1", UpdateCardRequest{CoverAttachmentID: Some("a1")})
	require.NoError(t, err)
	assert.JSONEq(t, `{"coverAttachmentId":"a1"}`, string(srv.Last().Body))

	_, err = c.UpdateCard(t.Context(), "c1", UpdateCardRequest{CoverAttachmentID: Null[string]()})
	require.NoError(t, err)
	assert.JSONEq(t, `{"coverAttachmentId":null}`, string(srv.Last().Body))
}

func TestAttachmentSizeMayBeAString(t *testing.T) {
	c, _ := newTestClient(t, map[string]any{"PATCH /api/attachments/a1": json.RawMessage(`{"item":{"id":"a1","type":"file","name":"x","data":{"size":"2048","image":null}}}`)})

	a, err := c.UpdateAttachment(t.Context(), "a1", "x")

	require.NoError(t, err)
	assert.Equal(t, "2048", a.Data.Size.String())
	assert.Nil(t, a.Data.Image)
}
