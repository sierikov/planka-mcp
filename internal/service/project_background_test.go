package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/sierikov/planka-mcp/internal/planka"
	"github.com/sierikov/planka-mcp/internal/unsplash"
)

// backgroundFixture is a projectsFixture whose photo source can search when
// canSearch says so, and whose project Home has the gradient ocean-dive.
func backgroundFixture(t *testing.T, canSearch bool) *projectsFixture {
	t.Helper()
	f := newProjectsFixture(t)
	f.photos.EXPECT().CanSearch().Return(canSearch).Maybe()
	projects := teamProjects()
	projects.Items[1].BackgroundType, projects.Items[1].BackgroundGradient = new("gradient"), new("ocean-dive")
	f.cardsFixture.projects.EXPECT().Projects(mock.Anything).Return(projects, nil).Maybe()
	return f
}

var lake = &unsplash.Photo{
	ID: "1Z2niiBPg5A", Filename: "unsplash-1Z2niiBPg5A.jpg", ContentType: "image/jpeg", Data: []byte("jpg"),
	Page: "https://unsplash.com/photos/1Z2niiBPg5A?utm", Attribution: "Photo by Jane Doe on Unsplash",
}

func TestSetBackgroundToAGradient(t *testing.T) {
	f := backgroundFixture(t, false)
	f.api.EXPECT().UpdateProject(mock.Anything, "pw", planka.UpdateProjectRequest{
		BackgroundType: planka.Some("gradient"), BackgroundGradient: planka.Some("sun-scream"),
	}).Return(&planka.Project{ID: "pw"}, nil).Once()

	got, err := f.projects.SetBackground(t.Context(), SetBackground{Project: "Work", Gradient: " sun-scream "})

	require.NoError(t, err)
	assert.Equal(t, &Background{Project: "Work", Type: "gradient", Gradient: "sun-scream"}, got)
}

func TestSetBackgroundThatIsAlreadySetWritesNothing(t *testing.T) {
	f := backgroundFixture(t, false)

	got, err := f.projects.SetBackground(t.Context(), SetBackground{Project: "Home", Gradient: "ocean-dive"})
	require.NoError(t, err)
	assert.Equal(t, "gradient", got.Type)

	got, err = f.projects.SetBackground(t.Context(), SetBackground{Project: "Work", None: true})
	require.NoError(t, err)
	assert.Equal(t, &Background{Project: "Work", Type: "none"}, got)
}

func TestSetBackgroundToNone(t *testing.T) {
	f := backgroundFixture(t, false)
	f.api.EXPECT().UpdateProject(mock.Anything, "ph", planka.UpdateProjectRequest{BackgroundType: planka.Null[string]()}).
		Return(&planka.Project{ID: "ph"}, nil).Once()

	got, err := f.projects.SetBackground(t.Context(), SetBackground{Project: "Home", None: true})

	require.NoError(t, err)
	assert.Equal(t, "none", got.Type)
}

// expectPhotoUpload expects the lake photo to be uploaded to Work and made
// its background.
func (f *projectsFixture) expectPhotoUpload() {
	f.api.EXPECT().CreateBackgroundImage(mock.Anything, "pw", planka.FileUpload{
		Filename: "unsplash-1Z2niiBPg5A.jpg", ContentType: "image/jpeg", Data: []byte("jpg"),
	}).Return(&planka.BackgroundImage{ID: "bg1", ProjectID: "pw"}, nil).Once()
	f.api.EXPECT().UpdateProject(mock.Anything, "pw", planka.UpdateProjectRequest{
		BackgroundType: planka.Some("image"), BackgroundImageID: planka.Some("bg1"),
	}).Return(&planka.Project{ID: "pw"}, nil).Once()
}

func TestSetBackgroundToAnUnsplashLink(t *testing.T) {
	f := backgroundFixture(t, false)
	f.photos.EXPECT().Link(mock.Anything, "https://unsplash.com/photos/green-hills-1Z2niiBPg5A").Return(lake, nil).Once()
	f.expectPhotoUpload()

	got, err := f.projects.SetBackground(t.Context(), SetBackground{Project: "Work", Unsplash: "https://unsplash.com/photos/green-hills-1Z2niiBPg5A"})

	require.NoError(t, err)
	assert.Equal(t, &Background{Project: "Work", Type: "image", ImageID: "bg1", Photo: lake.Page, Attribution: lake.Attribution}, got)
}

func TestSetBackgroundToAFoundPhoto(t *testing.T) {
	f := backgroundFixture(t, true)
	f.photos.EXPECT().Search(mock.Anything, "green hills").Return(lake, nil).Once()
	f.expectPhotoUpload()

	got, err := f.projects.SetBackground(t.Context(), SetBackground{Project: "Work", Search: "green hills"})

	require.NoError(t, err)
	assert.Equal(t, "Photo by Jane Doe on Unsplash", got.Attribution)
}

func TestSetBackgroundRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		in        SetBackground
		canSearch bool
		want      string
	}{
		"nothing":          {SetBackground{Project: "Work"}, false, "give exactly one of gradient, unsplash and none"},
		"two":              {SetBackground{Project: "Work", Gradient: "old-lime", None: true}, true, "give exactly one of gradient, unsplash, search and none"},
		"unknown gradient": {SetBackground{Project: "Work", Gradient: "rainbow"}, false, `unknown gradient "rainbow"; gradients: old-lime, ocean-dive`},
		"search, no key":   {SetBackground{Project: "Work", Search: "hills"}, false, "set UNSPLASH_ACCESS_KEY"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newProjectsFixture(t)
			f.photos.EXPECT().CanSearch().Return(tc.canSearch).Once()

			_, err := f.projects.SetBackground(t.Context(), tc.in)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestSetBackgroundPassesOnAPhotoError(t *testing.T) {
	f := backgroundFixture(t, false)
	f.photos.EXPECT().Link(mock.Anything, "x").Return(nil, errors.New(`"x" is not a link`)).Once()

	_, err := f.projects.SetBackground(t.Context(), SetBackground{Project: "Work", Unsplash: "x"})

	require.EqualError(t, err, `"x" is not a link`)
}

func TestSetBackgroundExplainsPlankasRefusals(t *testing.T) {
	f := backgroundFixture(t, false)
	f.photos.EXPECT().Link(mock.Anything, mock.Anything).Return(lake, nil)
	f.api.EXPECT().CreateBackgroundImage(mock.Anything, "pw", mock.Anything).Return(nil, apiError(http.StatusNotFound)).Once()

	_, err := f.projects.SetBackground(t.Context(), SetBackground{Project: "Work", Unsplash: "link"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `only a project manager of the project can change the background of project "Work"`)

	f.api.EXPECT().CreateBackgroundImage(mock.Anything, "pw", mock.Anything).Return(&planka.BackgroundImage{ID: "bg2"}, nil).Once()
	f.api.EXPECT().UpdateProject(mock.Anything, "pw", mock.Anything).Return(nil, apiError(http.StatusBadGateway)).Once()

	_, err = f.projects.SetBackground(t.Context(), SetBackground{Project: "Work", Unsplash: "link"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `the photo is in the image gallery of project "Work" (id bg2), but making it the background failed`)
}
