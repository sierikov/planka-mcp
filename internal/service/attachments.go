package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sierikov/planka-mcp/internal/planka"
)

const (
	// maxAttachmentName is the longest attachment name Planka stores, in
	// UTF-16 units.
	maxAttachmentName = 128
	// maxLinkURL is the longest link Planka stores.
	maxLinkURL = 2048
)

// Attachments adds, renames and deletes the files and links on cards, and
// picks a card's cover. Reading them is (*Cards).Get.
type Attachments struct {
	cards *Cards
	api   AttachmentsAPI
	// uploads allows reading files from this machine's disk.
	uploads bool
}

// NewAttachments returns an Attachments service. It finds cards through cards
// and writes through api. Only with uploads set does Add read local files: a
// server shared over HTTP runs on another machine than its users, so its disk
// holds none of their files.
func NewAttachments(cards *Cards, api AttachmentsAPI, uploads bool) *Attachments {
	return &Attachments{cards: cards, api: api, uploads: uploads}
}

// AddAttachment is an attachment for Card: the local file at Path or a link to
// URL, exactly one of them. Name is its title; by default the file's name or
// the URL.
type AddAttachment struct {
	Card      CardRef
	Path, URL string
	Name      string
}

// UpdateAttachment changes the attachment Attachment (exact name or ID) on
// Card. A nil field is left alone. Cover true makes the attachment the card's
// cover, which must be an image; false removes the cover if it is this one.
type UpdateAttachment struct {
	Card       CardRef
	Attachment string
	Name       *string
	Cover      *bool
}

// Add attaches a local file or a link to a card. A file is read whole before
// anything is sent (see readUpload). Cover is set on the result when Planka
// made the new file the card's cover, as it does with a card's first image.
func (a *Attachments) Add(ctx context.Context, in AddAttachment) (*Attachment, error) {
	path, link := strings.TrimSpace(in.Path), strings.TrimSpace(in.URL)
	switch {
	case path != "" && link != "":
		return nil, errors.New("give path or url, not both")
	case path != "":
		return a.addFile(ctx, in.Card, path, in.Name)
	case link != "":
		return a.addLink(ctx, in.Card, link, in.Name)
	}
	return nil, errors.New("give path (a file on this computer) or url (a link)")
}

func (a *Attachments) addFile(ctx context.Context, ref CardRef, path, name string) (*Attachment, error) {
	if !a.uploads {
		return nil, errors.New("this server runs on another machine and cannot read files from yours: attach a link with url instead")
	}
	f, err := readUpload(path)
	if err != nil {
		return nil, err
	}
	if name, err = attachmentName(name, f.name); err != nil {
		return nil, err
	}
	// No board lock: nothing here plans from the board, and an upload can
	// take long enough to hold up every other write on it.
	id, _, err := a.cards.resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	created, err := a.api.CreateFileAttachment(ctx, id, planka.FileUpload{
		Name: name, Filename: f.name, ContentType: f.contentType, Data: f.data,
	})
	if err != nil {
		return nil, notFound("card", id, err)
	}
	out := toAttachment(*created, "")
	if created.Data.Image != nil {
		// The file is attached, so a failed read only leaves Cover unset.
		if cr, err := a.cards.cards.Card(ctx, id); err == nil {
			out.Cover = deref(cr.Item.CoverAttachmentID) == created.ID
		}
	}
	return &out, nil
}

func (a *Attachments) addLink(ctx context.Context, ref CardRef, link, name string) (*Attachment, error) {
	if err := checkLink(link); err != nil {
		return nil, err
	}
	name, err := attachmentName(name, linkName(link))
	if err != nil {
		return nil, err
	}
	id, _, err := a.cards.resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	created, err := a.api.CreateLinkAttachment(ctx, id, name, link)
	if err != nil {
		return nil, notFound("card", id, err)
	}
	out := toAttachment(*created, "")
	return &out, nil
}

// Update renames an attachment, or makes it or stops it being the card's
// cover. Everything is checked before the first write.
func (a *Attachments) Update(ctx context.Context, in UpdateAttachment) (*Attachment, error) {
	if in.Name == nil && in.Cover == nil {
		return nil, errors.New("nothing to change: give name or cover")
	}
	var name string
	if in.Name != nil {
		var err error
		if name, err = attachmentName(*in.Name, ""); err != nil {
			return nil, err
		}
	}
	id, cr, unlock, err := a.lockAttachments(ctx, in.Card)
	if err != nil {
		return nil, err
	}
	defer unlock()
	target, err := exactAttachment(cr, in.Attachment)
	if err != nil {
		return nil, err
	}
	cover := deref(cr.Item.CoverAttachmentID)
	patch, err := coverPatch(target, cover, in.Cover)
	if err != nil {
		return nil, err
	}
	if in.Name == nil {
		name = target.Name
	}
	return a.applyUpdate(ctx, id, target, cover, name, patch)
}

// applyUpdate renames target, when name differs, then sends patch, the
// card's cover change, when there is one. cover is the card's cover ID before.
func (a *Attachments) applyUpdate(ctx context.Context, cardID string, target planka.Attachment, cover, name string, patch planka.UpdateCardRequest) (*Attachment, error) {
	out := toAttachment(target, cover)
	if name != target.Name {
		updated, err := a.api.UpdateAttachment(ctx, target.ID, name)
		if err != nil {
			return nil, notFound("attachment", target.ID, err)
		}
		out = toAttachment(*updated, cover)
	}
	if patch == (planka.UpdateCardRequest{}) {
		return &out, nil
	}
	if _, err := a.cards.write.UpdateCard(ctx, cardID, patch); err != nil {
		if out.Name != target.Name {
			return nil, fmt.Errorf("attachment renamed to %q, but changing the cover failed: %w", out.Name, err)
		}
		return nil, notFound("card", cardID, err)
	}
	out.Cover = !patch.CoverAttachmentID.Null
	return &out, nil
}

// Delete deletes an attachment, and a file's content with it. Planka removes
// the card's cover when it was this attachment. The result is the attachment
// as it was.
func (a *Attachments) Delete(ctx context.Context, card CardRef, attachment string) (*Attachment, error) {
	_, cr, unlock, err := a.lockAttachments(ctx, card)
	if err != nil {
		return nil, err
	}
	defer unlock()
	target, err := exactAttachment(cr, attachment)
	if err != nil {
		return nil, err
	}
	if err := a.api.DeleteAttachment(ctx, target.ID); err != nil {
		return nil, notFound("attachment", target.ID, err)
	}
	out := toAttachment(target, deref(cr.Item.CoverAttachmentID))
	return &out, nil
}

// lockAttachments finds a card, locks its board and reads the card under the
// lock.
func (a *Attachments) lockAttachments(ctx context.Context, ref CardRef) (id string, cr *planka.CardResponse, unlock func(), err error) {
	id, _, unlock, err = a.cards.lockCard(ctx, ref)
	if err != nil {
		return "", nil, nil, err
	}
	if cr, err = a.cards.cards.Card(ctx, id); err != nil {
		unlock()
		return "", nil, nil, notFound("card", id, err)
	}
	return id, cr, unlock, nil
}

// coverPatch is the card change that want asks of target, given the card's
// cover: none when the cover is already as asked.
func coverPatch(target planka.Attachment, cover string, want *bool) (planka.UpdateCardRequest, error) {
	var patch planka.UpdateCardRequest
	switch {
	case want == nil:
	case *want && cover != target.ID:
		if target.Type != planka.AttachmentFile || target.Data.Image == nil {
			return patch, fmt.Errorf("only an image can be the card's cover, and %q is not one", target.Name)
		}
		patch.CoverAttachmentID = planka.Some(target.ID)
	case !*want && cover == target.ID:
		patch.CoverAttachmentID = planka.Null[string]()
	}
	return patch, nil
}

// exactAttachment finds the attachment a write targets: by ID, or by its exact
// name, case and surrounding space ignored. A prefix does not count, and two
// attachments of the same name are an AmbiguousError.
func exactAttachment(cr *planka.CardResponse, ref string) (planka.Attachment, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return planka.Attachment{}, errors.New("attachment is empty: give its name or ID")
	}
	all := cr.Included.Attachments
	if at, ok := byID(ref, all, func(at planka.Attachment) string { return at.ID }); ok {
		return at, nil
	}
	var found []planka.Attachment
	names := make([]string, len(all))
	for i, at := range all {
		names[i] = at.Name
		if normalize(at.Name) == normalize(ref) {
			found = append(found, at)
		}
	}
	switch len(found) {
	case 0:
		return planka.Attachment{}, &UnknownNameError{Kind: "attachment", Name: ref, Scope: cardScope(cr), Valid: names}
	case 1:
		return found[0], nil
	}
	cands := make([]Candidate, len(found))
	for i, at := range found {
		cands[i] = Candidate{ID: at.ID, Label: at.Name}
	}
	return planka.Attachment{}, &AmbiguousError{Kind: "attachment", Query: ref, Candidates: cands}
}

// attachmentName trims name, or takes fallback when it is empty, and checks
// the result fits Planka.
func attachmentName(name, fallback string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = fallback
	}
	switch {
	case name == "":
		return "", errors.New("attachment name is empty")
	case jsLen(name) > maxAttachmentName && name == fallback:
		return "", fmt.Errorf("the default name %q is longer than %d characters: give a shorter name", name, maxAttachmentName)
	case jsLen(name) > maxAttachmentName:
		return "", fmt.Errorf("attachment name is longer than %d characters", maxAttachmentName)
	}
	return name, nil
}

// toAttachment converts a Planka attachment; cover is the card's cover ID.
func toAttachment(at planka.Attachment, cover string) Attachment {
	out := Attachment{ID: at.ID, Name: at.Name, Type: at.Type, URL: at.Data.URL, Cover: at.ID != "" && at.ID == cover}
	if n, err := at.Data.Size.Int64(); err == nil {
		out.Size = n
	}
	return out
}
