package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/sierikov/planka-mcp/internal/planka"
	"github.com/sierikov/planka-mcp/internal/unsplash"
)

// BackgroundGradients are the gradients Planka 2.1 and 2.2 offer as a
// project background.
var BackgroundGradients = []string{
	"old-lime", "ocean-dive", "tzepesch-style", "jungle-mesh", "strawberry-dust", "purple-rose", "sun-scream",
	"warm-rust", "sky-change", "green-eyes", "blue-xchange", "blood-orange", "sour-peel", "green-ninja",
	"algae-green", "coral-reef", "steel-grey", "heat-waves", "velvet-lounge", "purple-rain", "blue-steel",
	"blueish-curve", "prism-light", "green-mist", "red-curtain",
}

// Background types, as Planka names them; "none" is a project without one.
const (
	backgroundGradient = "gradient"
	backgroundImage    = "image"
	backgroundNone     = "none"
)

// SetBackground sets the background of Project (ID or name) to exactly one
// of: a Gradient, the photo an Unsplash link names, the Unsplash photo that
// best matches Search, or None.
type SetBackground struct {
	Project                    string
	Gradient, Unsplash, Search string
	None                       bool
}

// Background is a project's background after a change. Type is gradient,
// image or none. Photo links to the Unsplash photo and Attribution names its
// photographer, when known.
type Background struct {
	Project, Type, Gradient, ImageID string
	Photo, Attribution               string
}

// SetBackground changes a project's background. A photo is fetched from
// Unsplash, uploaded to the project's image gallery, where the images it
// replaces stay, and then made the background.
func (s *Projects) SetBackground(ctx context.Context, in SetBackground) (*Background, error) {
	in.Gradient, in.Unsplash, in.Search = strings.TrimSpace(in.Gradient), strings.TrimSpace(in.Unsplash), strings.TrimSpace(in.Search)
	if err := checkBackground(in, s.photos.CanSearch()); err != nil {
		return nil, err
	}
	resp, err := s.cards.catalog.projects.Projects(ctx)
	if err != nil {
		return nil, err
	}
	p, err := pickProject(resp, in.Project)
	if err != nil {
		return nil, err
	}
	switch {
	case in.None:
		out := &Background{Project: p.Name, Type: backgroundNone}
		if p.BackgroundType == nil {
			return out, nil
		}
		return out, s.patchBackground(ctx, p, planka.UpdateProjectRequest{BackgroundType: planka.Null[string]()})
	case in.Gradient != "":
		out := &Background{Project: p.Name, Type: backgroundGradient, Gradient: in.Gradient}
		if deref(p.BackgroundType) == backgroundGradient && deref(p.BackgroundGradient) == in.Gradient {
			return out, nil
		}
		patch := planka.UpdateProjectRequest{BackgroundType: planka.Some(backgroundGradient), BackgroundGradient: planka.Some(in.Gradient)}
		return out, s.patchBackground(ctx, p, patch)
	}
	return s.setPhoto(ctx, p, in)
}

// setPhoto fetches the photo in asks for, uploads it to the project and makes
// it the background.
func (s *Projects) setPhoto(ctx context.Context, p planka.Project, in SetBackground) (*Background, error) {
	var photo *unsplash.Photo
	var err error
	if in.Search != "" {
		photo, err = s.photos.Search(ctx, in.Search)
	} else {
		photo, err = s.photos.Link(ctx, in.Unsplash)
	}
	if err != nil {
		return nil, err
	}
	img, err := s.api.CreateBackgroundImage(ctx, p.ID, planka.FileUpload{Filename: photo.Filename, ContentType: photo.ContentType, Data: photo.Data})
	if err != nil {
		var api *planka.APIError
		if errors.As(err, &api) && api.Status == http.StatusUnprocessableEntity {
			return nil, fmt.Errorf("the photo was refused by Planka (HTTP 422): %s", api.Body)
		}
		return nil, managerOnly(err, fmt.Sprintf("change the background of project %q", p.Name))
	}
	patch := planka.UpdateProjectRequest{BackgroundType: planka.Some(backgroundImage), BackgroundImageID: planka.Some(img.ID)}
	if err := s.patchBackground(ctx, p, patch); err != nil {
		return nil, fmt.Errorf("the photo is in the image gallery of project %q (id %s), but making it the background failed: %w", p.Name, img.ID, err)
	}
	return &Background{Project: p.Name, Type: backgroundImage, ImageID: img.ID, Photo: photo.Page, Attribution: photo.Attribution}, nil
}

func (s *Projects) patchBackground(ctx context.Context, p planka.Project, patch planka.UpdateProjectRequest) error {
	if _, err := s.api.UpdateProject(ctx, p.ID, patch); err != nil {
		return managerOnly(err, fmt.Sprintf("change the background of project %q", p.Name))
	}
	return nil
}

// checkBackground checks that exactly one background is given, before
// anything is read. canSearch says whether search is offered.
func checkBackground(in SetBackground, canSearch bool) error {
	given := 0
	for _, set := range []bool{in.Gradient != "", in.Unsplash != "", in.Search != "", in.None} {
		if set {
			given++
		}
	}
	if given != 1 {
		if canSearch {
			return errors.New("give exactly one of gradient, unsplash, search and none")
		}
		return errors.New("give exactly one of gradient, unsplash and none")
	}
	switch {
	case in.Gradient != "" && !slices.Contains(BackgroundGradients, in.Gradient):
		return fmt.Errorf("unknown gradient %q; gradients: %s", in.Gradient, strings.Join(BackgroundGradients, ", "))
	case in.Search != "" && !canSearch:
		return unsplash.ErrNoKey
	}
	return nil
}
