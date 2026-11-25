package planka

import (
	"bytes"
	"cmp"
	"context"
	"mime/multipart"
	"net/http"
	"net/textproto"
)

// FileUpload is a file to attach to a card. Name is the attachment's title,
// Filename the name the file is stored and downloaded under. Planka works out
// the file's type from its content; ContentType is only a hint.
type FileUpload struct {
	Name, Filename, ContentType string
	Data                        []byte
}

// CreateFileAttachment uploads a file to a card. Planka makes thumbnails of an
// image, and makes it the card's cover when the card has none.
func (c *Client) CreateFileAttachment(ctx context.Context, cardID string, f FileUpload) (*Attachment, error) {
	body, err := multipartFile(f)
	if err != nil {
		return nil, err
	}
	return send[Attachment](ctx, c, http.MethodPost, "/api/cards/"+pathParam(cardID)+"/attachments", body)
}

// CreateLinkAttachment attaches a link to a card.
func (c *Client) CreateLinkAttachment(ctx context.Context, cardID, name, url string) (*Attachment, error) {
	body := map[string]string{"type": AttachmentLink, "name": name, "url": url}
	return send[Attachment](ctx, c, http.MethodPost, "/api/cards/"+pathParam(cardID)+"/attachments", body)
}

// UpdateAttachment renames an attachment.
func (c *Client) UpdateAttachment(ctx context.Context, id, name string) (*Attachment, error) {
	body := map[string]string{"name": name}
	return send[Attachment](ctx, c, http.MethodPatch, "/api/attachments/"+pathParam(id), body)
}

// DeleteAttachment deletes an attachment, and its file. Planka removes the
// card's cover when it was this attachment.
func (c *Client) DeleteAttachment(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/attachments/"+pathParam(id), nil, nil, nil)
}

// multipartFile is the form Planka takes a file in. The text fields go first:
// Planka reads the form as a stream and does not wait for fields that come
// after the file.
func multipartFile(f FileUpload) (rawBody, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, field := range [][2]string{{"type", AttachmentFile}, {"name", f.Name}} {
		if err := w.WriteField(field[0], field[1]); err != nil {
			return rawBody{}, err
		}
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", multipart.FileContentDisposition("file", f.Filename))
	h.Set("Content-Type", cmp.Or(f.ContentType, "application/octet-stream"))
	part, err := w.CreatePart(h)
	if err != nil {
		return rawBody{}, err
	}
	if _, err := part.Write(f.Data); err != nil {
		return rawBody{}, err
	}
	if err := w.Close(); err != nil {
		return rawBody{}, err
	}
	return rawBody{contentType: w.FormDataContentType(), data: buf.Bytes()}, nil
}
