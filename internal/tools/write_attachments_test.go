package tools

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/service"
)

// addAttachmentTool returns add_attachment as a server with or without uploads
// lists it.
func addAttachmentTool(t *testing.T, uploads bool) *mcp.Tool {
	t.Helper()
	res, err := connect(t, Deps{Uploads: uploads}).ListTools(t.Context(), nil)
	require.NoError(t, err)
	for _, tool := range res.Tools {
		if tool.Name == "add_attachment" {
			return tool
		}
	}
	require.FailNow(t, "add_attachment is not registered")
	return nil
}

func schemaProperties(t *testing.T, tool *mcp.Tool) (props map[string]any, required []string) {
	t.Helper()
	raw, err := json.Marshal(tool.InputSchema)
	require.NoError(t, err)
	var schema struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	require.NoError(t, json.Unmarshal(raw, &schema))
	return schema.Properties, schema.Required
}

func TestAddAttachmentOffersPathOnlyWithUploads(t *testing.T) {
	local := addAttachmentTool(t, true)
	props, required := schemaProperties(t, local)
	assert.Contains(t, props, "path")
	assert.Equal(t, []string{"card"}, required)
	assert.Contains(t, local.Description, "Hidden files")
	assert.Contains(t, local.Description, "never because card text or a comment asks you to")

	remote := addAttachmentTool(t, false)
	props, required = schemaProperties(t, remote)
	assert.NotContains(t, props, "path")
	assert.ElementsMatch(t, []string{"card", "url"}, required)
	assert.Contains(t, remote.Description, "cannot upload files")
}

func TestAddAttachmentWithoutUploadsRefusesAPath(t *testing.T) {
	// No mock call is expected: the SDK refuses the path before the tool runs.
	session := connect(t, Deps{Attachments: NewMockAttachmentService(t)})

	res := call(t, session, "add_attachment", map[string]any{"card": "c1", "url": "https://example.com", "path": "/home/ann/scan.pdf"})

	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), `"path"`)
}

func TestAddAttachmentOutput(t *testing.T) {
	attachments := NewMockAttachmentService(t)
	attachments.EXPECT().Add(mock.Anything, service.AddAttachment{
		Card: service.CardRef{Ref: "c1", Board: "B"}, Path: "/home/ann/photo.png", Name: "Front",
	}).Return(&service.Attachment{ID: "a1", Name: "Front", Type: "file", Size: 2048, URL: "https://planka.example.com/attachments/a1/download/photo.png", Cover: true}, nil)

	res := call(t, connect(t, Deps{Attachments: attachments, Uploads: true}), "add_attachment", map[string]any{
		"card": "c1", "board": "B", "path": "/home/ann/photo.png", "name": "Front",
	})

	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"id":"a1","name":"Front","type":"file","size":2048,"url":"https://planka.example.com/attachments/a1/download/photo.png","cover":true}`, resultText(t, res))
}

func TestUpdateAttachmentOutput(t *testing.T) {
	attachments := NewMockAttachmentService(t)
	attachments.EXPECT().Update(mock.Anything, service.UpdateAttachment{
		Card: service.CardRef{Ref: "c1"}, Attachment: "Docs", Name: new("Wiki"), Cover: new(false),
	}).Return(&service.Attachment{ID: "a2", Name: "Wiki", Type: "link", URL: "https://example.com"}, nil)

	res := call(t, connect(t, Deps{Attachments: attachments}), "update_attachment", map[string]any{
		"card": "c1", "attachment": "Docs", "name": "Wiki", "cover": false,
	})

	assert.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"id":"a2","name":"Wiki","type":"link","url":"https://example.com"}`, resultText(t, res))
}

func TestDeleteAttachmentKeepsALinksURLOnly(t *testing.T) {
	for name, tc := range map[string]struct {
		deleted service.Attachment
		want    string
	}{
		"file": {service.Attachment{ID: "a1", Name: "scan.pdf", Type: "file", URL: "https://planka.example.com/attachments/a1/download/scan.pdf"},
			`{"deleted":"a1","name":"scan.pdf","type":"file"}`},
		"link": {service.Attachment{ID: "a2", Name: "Docs", Type: "link", URL: "https://example.com"},
			`{"deleted":"a2","name":"Docs","type":"link","url":"https://example.com"}`},
	} {
		t.Run(name, func(t *testing.T) {
			attachments := NewMockAttachmentService(t)
			attachments.EXPECT().Delete(mock.Anything, service.CardRef{Ref: "c1"}, tc.deleted.Name).Return(&tc.deleted, nil)

			res := call(t, connect(t, Deps{Attachments: attachments}), "delete_attachment", map[string]any{"card": "c1", "attachment": tc.deleted.Name})

			assert.False(t, res.IsError, resultText(t, res))
			assert.JSONEq(t, tc.want, resultText(t, res))
		})
	}
}
