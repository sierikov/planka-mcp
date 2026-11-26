//go:build e2e

package e2e

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	plankaapi "github.com/sierikov/planka-mcp/internal/planka"
)

type backgroundResult struct {
	Project    string `json:"project"`
	Background string `json:"background"`
	Gradient   string `json:"gradient"`
	ImageID    string `json:"image_id"`
	Photo      string `json:"photo"`
	Credit     string `json:"credit"`
}

// projectBackground reads a project's background type, gradient and image ID
// from Planka.
func (p *planka) projectBackground(t *testing.T, id string) (kind, gradient, imageID string) {
	t.Helper()
	var out struct {
		Item struct {
			BackgroundType     *string `json:"backgroundType"`
			BackgroundGradient *string `json:"backgroundGradient"`
			BackgroundImageID  *string `json:"backgroundImageId"`
		} `json:"item"`
	}
	require.NoError(t, p.request(t.Context(), http.MethodGet, "/projects/"+id, "", nil, &out))
	str := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	return str(out.Item.BackgroundType), str(out.Item.BackgroundGradient), str(out.Item.BackgroundImageID)
}

// TestProjectBackgroundGradient sets a gradient, refuses an unknown one, and
// removes the background.
func TestProjectBackgroundGradient(t *testing.T) {
	p := requirePlanka(t)
	project := p.newProject(t, "e2e-bg-"+unique(t))
	s := connectStdio(t, env.key)

	got := callInto[backgroundResult](t, s, "set_project_background", map[string]any{"project": project, "gradient": "ocean-dive"})
	assert.Equal(t, "gradient", got.Background)
	kind, gradient, _ := p.projectBackground(t, project)
	assert.Equal(t, "gradient", kind)
	assert.Equal(t, "ocean-dive", gradient)

	text, isError := call(t, s, "set_project_background", map[string]any{"project": project, "gradient": "rainbow"})
	assert.True(t, isError, text)

	text, isError = call(t, s, "set_project_background", map[string]any{"project": project, "search": "hills"})
	assert.True(t, isError, "without UNSPLASH_ACCESS_KEY there is no search: %s", text)

	text, isError = call(t, s, "set_project_background", map[string]any{"project": project, "unsplash": "https://unsplash.com/photos/green-hills-1Z2niiBPg5A"})
	assert.True(t, isError, "without UNSPLASH_ACCESS_KEY a photo page is refused before Unsplash is asked: %s", text)
	assert.Contains(t, text, "images.unsplash.com/photo-")

	got = callInto[backgroundResult](t, s, "set_project_background", map[string]any{"project": project, "none": true})
	assert.Equal(t, "none", got.Background)
	kind, _, _ = p.projectBackground(t, project)
	assert.Empty(t, kind)
}

// TestProjectBackgroundImageUpload uploads an image the way
// set_project_background does after fetching a photo, and makes it the
// background: Planka takes the multipart form and the PATCH.
func TestProjectBackgroundImageUpload(t *testing.T) {
	p := requirePlanka(t)
	project := p.newProject(t, "e2e-bgimg-"+unique(t))
	c := plankaapi.NewClient(env.url, plankaapi.WithStaticKey(env.key))

	img, err := c.CreateBackgroundImage(t.Context(), project, plankaapi.FileUpload{Filename: "sky.png", ContentType: "image/png", Data: testPNG(t)})
	require.NoError(t, err)
	_, err = c.UpdateProject(t.Context(), project, plankaapi.UpdateProjectRequest{
		BackgroundType: plankaapi.Some("image"), BackgroundImageID: plankaapi.Some(img.ID),
	})
	require.NoError(t, err)

	kind, _, imageID := p.projectBackground(t, project)
	assert.Equal(t, "image", kind)
	assert.Equal(t, img.ID, imageID)
}

// TestProjectBackgroundFromUnsplash sets a real Unsplash photo from its image
// link, which needs no key. It downloads about 0.7 MB from Unsplash, so it
// runs only with E2E_UNSPLASH=1.
func TestProjectBackgroundFromUnsplash(t *testing.T) {
	if os.Getenv("E2E_UNSPLASH") == "" {
		t.Skip("set E2E_UNSPLASH=1 to fetch a real photo from Unsplash")
	}
	p := requirePlanka(t)
	project := p.newProject(t, "e2e-bgphoto-"+unique(t))
	s := connectStdio(t, env.key)

	got := callInto[backgroundResult](t, s, "set_project_background", map[string]any{
		"project": project, "unsplash": "https://images.unsplash.com/photo-1470071459604-3b5ec3a7fe05?ixlib=rb-4.1.0&w=6000",
	})
	assert.Equal(t, "image", got.Background)
	assert.Empty(t, got.Credit, "an image link does not say who took the photo")
	kind, _, imageID := p.projectBackground(t, project)
	assert.Equal(t, "image", kind)
	assert.Equal(t, got.ImageID, imageID)
}

// TestProjectBackgroundFromUnsplashAPI sets Unsplash photos from a photo page
// link and from a search, which go through the Unsplash API. It runs only
// with E2E_UNSPLASH=1 and UNSPLASH_ACCESS_KEY, which the server under test
// then gets; it downloads about 1.4 MB from Unsplash.
func TestProjectBackgroundFromUnsplashAPI(t *testing.T) {
	key := os.Getenv("UNSPLASH_ACCESS_KEY")
	if os.Getenv("E2E_UNSPLASH") == "" || key == "" {
		t.Skip("set E2E_UNSPLASH=1 and UNSPLASH_ACCESS_KEY to use the Unsplash API")
	}
	p := requirePlanka(t)
	project := p.newProject(t, "e2e-bgapi-"+unique(t))
	s := connectStdioEnv(t, env.key, "UNSPLASH_ACCESS_KEY="+key)

	page := callInto[backgroundResult](t, s, "set_project_background", map[string]any{
		"project": project, "unsplash": "https://unsplash.com/photos/green-hills-1Z2niiBPg5A",
	})
	assert.Equal(t, "image", page.Background)
	assert.Contains(t, page.Photo, "unsplash.com/photos/")
	assert.Contains(t, page.Photo, "1Z2niiBPg5A")
	assert.Regexp(t, `^Photo by .+ on Unsplash$`, page.Credit)

	found := callInto[backgroundResult](t, s, "set_project_background", map[string]any{"project": project, "search": "misty mountains"})
	assert.Equal(t, "image", found.Background)
	assert.Contains(t, found.Photo, "unsplash.com/photos/")
	assert.Regexp(t, `^Photo by .+ on Unsplash$`, found.Credit)
	assert.NotEqual(t, page.ImageID, found.ImageID)
	kind, _, imageID := p.projectBackground(t, project)
	assert.Equal(t, "image", kind)
	assert.Equal(t, found.ImageID, imageID)
}

// testPNG is a small sky-blue PNG.
func testPNG(t *testing.T) []byte {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for x := range 64 {
		for y := range 32 {
			m.Set(x, y, color.RGBA{R: 120, G: 180, B: 240, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, m))
	return buf.Bytes()
}
