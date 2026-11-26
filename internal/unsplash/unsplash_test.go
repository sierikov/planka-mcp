package unsplash

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// site is the host photo pages are on in the tests; it is never asked.
const site = "https://unsplash.example"

// fakeUnsplash stands in for images.unsplash.com and api.unsplash.com. Its
// image server answers any free photo path with a small JPEG when asked for it
// at Width, and records each path it served. Its API knows one photo,
// 1Z2niiBPg5A, and counts the downloads reported to it.
type fakeUnsplash struct {
	images, api *httptest.Server
	served      []string
	pinged      atomic.Int32
}

var jpeg = []byte{0xff, 0xd8, 0xff, 0xe0}

// newFake starts a fake Unsplash whose API answers with api, after checking
// the access key and handling the photo 1Z2niiBPg5A and download reports.
func newFake(t *testing.T, key string, api http.HandlerFunc) (*fakeUnsplash, *Client) {
	t.Helper()
	f := &fakeUnsplash{}
	f.images = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("w") != "2560" || r.URL.Query().Get("fm") != "jpg" {
			http.Error(w, "full size", http.StatusBadRequest)
			return
		}
		f.served = append(f.served, r.URL.Path)
		if r.URL.Path == "/photo-html" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>"))
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(jpeg)
	}))
	f.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Client-ID key" || r.Header.Get("Accept-Version") != "v1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/download"):
			f.pinged.Add(1)
			_, _ = w.Write([]byte(`{"url":"x"}`))
		case r.URL.Path == "/photos/1Z2niiBPg5A":
			_, _ = w.Write([]byte(f.photo("1Z2niiBPg5A", f.images.URL+"/photo-1470071459604-3b5ec3a7fe05?ixid=x")))
		case api != nil:
			api(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(func() { f.images.Close(); f.api.Close() })
	return f, newClient(key, site, f.images.URL, f.api.URL)
}

// photo is the API's description of a photo by Jane Doe whose image is raw.
func (f *fakeUnsplash) photo(id, raw string) string {
	return `{"id":"` + id + `","urls":{"raw":"` + raw + `"},` +
		`"links":{"html":"https://unsplash.com/photos/` + id + `","download_location":"` + f.api.URL + `/photos/` + id + `/download?ixid=1"},` +
		`"user":{"name":"Jane Doe"}}`
}

func TestPhotoID(t *testing.T) {
	for p, want := range map[string]string{
		"/photos/1Z2niiBPg5A":                       "1Z2niiBPg5A",
		"/photos/green-hills-at-dawn-1Z2niiBPg5A":   "1Z2niiBPg5A",
		"/photos/a-b--yBWo3mm5Fc":                   "-yBWo3mm5Fc",
		"/de/fotos/grune-hugel-1Z2niiBPg5A":         "1Z2niiBPg5A",
		"/photos/green-hills-1Z2niiBPg5A/download/": "1Z2niiBPg5A",
		"/illustrations/a-cat-1Z2niiBPg5A":          "1Z2niiBPg5A",
		"/collections/123456/mountains":             "",
		"/s/photos/mountains":                       "",
		"/@v2osk":                                   "",
		"/photos/":                                  "",
		"/photos/longwordwithoutdash12345":          "",
	} {
		got, ok := photoID(p)
		assert.Equal(t, want, got, p)
		assert.Equal(t, want != "", ok, p)
	}
}

func TestLinkToAPhotoPage(t *testing.T) {
	f, c := newFake(t, "key", nil)

	p, err := c.Link(t.Context(), site+"/photos/green-hills-1Z2niiBPg5A")

	require.NoError(t, err)
	assert.Equal(t, jpeg, p.Data)
	assert.Equal(t, "image/jpeg", p.ContentType)
	assert.Equal(t, "1Z2niiBPg5A", p.ID)
	assert.Equal(t, "unsplash-1Z2niiBPg5A.jpg", p.Filename)
	assert.Equal(t, "https://unsplash.com/photos/1Z2niiBPg5A?"+utm, p.Page)
	assert.Equal(t, "Photo by Jane Doe on Unsplash", p.Attribution)
	assert.Equal(t, []string{"/photo-1470071459604-3b5ec3a7fe05"}, f.served, "the image is fetched resized, without Unsplash's own query")
	assert.Equal(t, int32(1), f.pinged.Load(), "the download is reported")
}

func TestLinkToAPhotoPageNeedsAKey(t *testing.T) {
	f, c := newFake(t, "", nil)

	_, err := c.Link(t.Context(), site+"/photos/green-hills-1Z2niiBPg5A")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "set UNSPLASH_ACCESS_KEY")
	assert.Contains(t, err.Error(), "images.unsplash.com/photo-")
	assert.Empty(t, f.served)
}

func TestLinkToAnImage(t *testing.T) {
	f, c := newFake(t, "", nil)

	p, err := c.Link(t.Context(), f.images.URL+"/photo-1506744038136?ixlib=rb-4.1.0&w=6000")

	require.NoError(t, err)
	assert.Equal(t, "photo-1506744038136.jpg", p.Filename)
	assert.Empty(t, p.Page)
	assert.Equal(t, []string{"/photo-1506744038136"}, f.served)
}

func TestLinkRefuses(t *testing.T) {
	var f *fakeUnsplash
	f, c := newFake(t, "key", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/photos/premiumAAAA":
			_, _ = w.Write([]byte(f.photo("premiumAAAA", "https://plus.unsplash.com/premium_photo-1?x=1")))
		case "/photos/elsewhereAA":
			_, _ = w.Write([]byte(f.photo("elsewhereAA", "https://evil.example.com/photo-1")))
		case "/photos/htmlAAAAAAA":
			_, _ = w.Write([]byte(f.photo("htmlAAAAAAA", f.images.URL+"/photo-html")))
		default:
			http.NotFound(w, r)
		}
	})

	for link, want := range map[string]string{
		"https://example.com/photos/1Z2niiBPg5A":    "is not an Unsplash link",
		"unsplash photo of a lake":                  "is not a link",
		site + "/collections/1/mountains":           "is not a link to one Unsplash photo",
		site + "/photos/missingAAAA":                `no Unsplash photo "missingAAAA"`,
		site + "/photos/premiumAAAA":                "Unsplash+ photo",
		site + "/photos/elsewhereAA":                `unexpected image link "https://evil.example.com/photo-1"`,
		site + "/photos/htmlAAAAAAA":                "not an image",
		f.images.URL + "/premium_photo-1":           "Unsplash+ photo",
		f.images.URL + "/profile-1":                 "is not an Unsplash photo",
		"https://plus.unsplash.com/premium_photo-1": "Unsplash+ photo",
	} {
		_, err := c.Link(t.Context(), link)
		require.Error(t, err, link)
		assert.Contains(t, err.Error(), want, link)
	}
	assert.Equal(t, []string{"/photo-html"}, f.served, "only the free image on Unsplash's image host is fetched")
}

func TestSearchFetchesTheFirstFreeLandscapePhotoAndPings(t *testing.T) {
	var f *fakeUnsplash
	f, c := newFake(t, "key", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/photos" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		assert.Equal(t, "misty mountains", q.Get("query"))
		assert.Equal(t, "landscape", q.Get("orientation"))
		assert.Equal(t, "10", q.Get("per_page"))
		_, _ = w.Write([]byte(`{"total":3,"results":[` +
			f.photo("premiumAAAA", "https://plus.unsplash.com/premium_photo-1") + `,` +
			f.photo("abcdefghijk", f.images.URL+"/photo-42?ixid=1") + `,` +
			f.photo("zzzzzzzzzzz", f.images.URL+"/photo-43") + `]}`))
	})

	p, err := c.Search(t.Context(), " misty mountains ")

	require.NoError(t, err)
	assert.Equal(t, "abcdefghijk", p.ID)
	assert.Equal(t, "Photo by Jane Doe on Unsplash", p.Attribution)
	assert.Equal(t, "https://unsplash.com/photos/abcdefghijk?"+utm, p.Page)
	assert.Equal(t, []string{"/photo-42"}, f.served)
	assert.Equal(t, int32(1), f.pinged.Load(), "the download is reported")
}

func TestSearchErrors(t *testing.T) {
	_, err := New("").Search(t.Context(), "x")
	require.ErrorIs(t, err, ErrNoKey)
	assert.False(t, New(" ").CanSearch())

	for status, want := range map[int]string{
		http.StatusUnauthorized: "check UNSPLASH_ACCESS_KEY",
		http.StatusForbidden:    "used up its hourly requests",
		http.StatusOK:           `no free landscape photo on Unsplash for "nothing"`,
		http.StatusBadGateway:   "got HTTP 502 from Unsplash",
	} {
		_, c := newFake(t, "key", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"total":0,"results":[]}`))
		})
		_, err := c.Search(t.Context(), "nothing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), want)
	}

	_, c := newFake(t, "wrong", nil)
	_, err = c.Search(t.Context(), "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check UNSPLASH_ACCESS_KEY")
}

func TestSearchSkipsImagesElsewhere(t *testing.T) {
	f, c := newFake(t, "key", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"id":"a","urls":{"raw":"https://evil.example.com/photo-1"}}]}`))
	})

	_, err := c.Search(t.Context(), "x")

	require.Error(t, err)
	assert.Contains(t, err.Error(), `no free landscape photo on Unsplash for "x"`)
	assert.Empty(t, f.served)
}
