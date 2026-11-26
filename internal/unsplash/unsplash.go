// Package unsplash fetches photos from Unsplash for project backgrounds. An
// image link (images.unsplash.com/photo-…) needs no key; a photo page
// (unsplash.com/photos/…) and finding a photo by words go through the
// Unsplash API, which needs an access key. Only images.unsplash.com and
// api.unsplash.com are ever asked, so no link or answer can make the server
// fetch anything else.
package unsplash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	// Width is the width a photo is fetched at: enough for a large screen,
	// and about 0.7 MB as a JPEG.
	Width = 2560
	// maxPhotoBytes bounds a fetched photo, maxJSONBytes an API answer.
	maxPhotoBytes = 20 << 20
	maxJSONBytes  = 1 << 20
	// idLen is the length of an Unsplash photo ID.
	idLen = 11
	// searchPage is how many photos a search reads to find a free one.
	searchPage = 10
	// linkHint says which links Link takes.
	linkHint = "give an Unsplash photo link (unsplash.com/photos/… or images.unsplash.com/photo-…)"
	// utm marks links back to Unsplash, as its guidelines ask.
	utm = "utm_source=planka-mcp&utm_medium=referral"
)

// ErrNoKey reports a search without an access key.
var ErrNoKey = errors.New("searching Unsplash needs an access key: set UNSPLASH_ACCESS_KEY")

// errPlus refuses an Unsplash+ photo, which needs a paid license.
var errPlus = errors.New("that is an Unsplash+ photo, which needs a paid license: pick a free one")

// errNotFound is the API's answer for a photo it does not have.
var errNotFound = errors.New("not found on Unsplash")

// Photo is a fetched photo. ID, Page and Attribution are set when known.
type Photo struct {
	ID, Filename, ContentType string
	Data                      []byte
	Page, Attribution         string
}

// Client fetches photos. Its zero value is not usable; call New.
type Client struct {
	key               string
	hc                *http.Client
	site, images, api *url.URL
}

// New returns a Client. An empty key allows links only.
func New(key string) *Client {
	return newClient(key, "https://unsplash.com", "https://images.unsplash.com", "https://api.unsplash.com")
}

func newClient(key, site, images, api string) *Client {
	return &Client{
		key: strings.TrimSpace(key),
		hc: &http.Client{
			Timeout: time.Minute,
			// A redirect is read, never followed: where it points is checked first.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		site: mustParse(site), images: mustParse(images), api: mustParse(api),
	}
}

func mustParse(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// CanSearch reports whether Search works and photo pages can be read: both
// need an access key.
func (c *Client) CanSearch() bool { return c.key != "" }

// Link fetches the photo an Unsplash link names: an image
// (images.unsplash.com/photo-…), or, with an access key, a photo page
// (unsplash.com/photos/…, in any of its languages). The pages themselves are
// not read: they turn away programs, so a page's photo is looked up in the API.
func (c *Client) Link(ctx context.Context, link string) (*Photo, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("%q is not a link: %s", link, linkHint)
	}
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	switch host {
	case c.images.Host:
		if err := checkImagePath(u.Path); err != nil {
			return nil, err
		}
		return c.fetch(ctx, u.Path)
	case c.site.Host:
		id, ok := photoID(u.Path)
		if !ok {
			return nil, fmt.Errorf("%q is not a link to one Unsplash photo: %s", link, linkHint)
		}
		return c.photoPage(ctx, id)
	case "plus.unsplash.com":
		return nil, errPlus
	}
	return nil, fmt.Errorf("%q is not an Unsplash link: %s", link, linkHint)
}

// Search fetches the free landscape photo Unsplash ranks first for words.
func (c *Client) Search(ctx context.Context, words string) (*Photo, error) {
	if !c.CanSearch() {
		return nil, ErrNoKey
	}
	words = strings.TrimSpace(words)
	if words == "" {
		return nil, errors.New("search is empty: give some words, for example \"misty mountains\"")
	}
	u := c.api.JoinPath("search", "photos")
	u.RawQuery = url.Values{
		"query": {words}, "per_page": {strconv.Itoa(searchPage)}, "orientation": {"landscape"}, "content_filter": {"high"},
	}.Encode()
	var out struct {
		Results []apiPhoto `json:"results"`
	}
	if err := c.apiGet(ctx, u.String(), &out); err != nil {
		return nil, err
	}
	for _, found := range out.Results {
		if c.imagePath(found) != "" {
			return c.take(ctx, found)
		}
	}
	return nil, fmt.Errorf("no free landscape photo on Unsplash for %q: try other words", words)
}

// photoPage fetches the photo with an ID, looked up in the API.
func (c *Client) photoPage(ctx context.Context, id string) (*Photo, error) {
	if !c.CanSearch() {
		return nil, errors.New("reading an unsplash.com photo page needs an access key: set UNSPLASH_ACCESS_KEY, " +
			"or give the photo's image link instead (images.unsplash.com/photo-…, from \"Copy image address\" on the photo)")
	}
	var found apiPhoto
	err := c.apiGet(ctx, c.api.JoinPath("photos", id).String(), &found)
	if errors.Is(err, errNotFound) {
		return nil, fmt.Errorf("no Unsplash photo %q", id)
	}
	if err != nil {
		return nil, err
	}
	return c.take(ctx, found)
}

// take fetches a photo the API described, and tells Unsplash it was
// downloaded, as its guidelines ask.
func (c *Client) take(ctx context.Context, found apiPhoto) (*Photo, error) {
	imagePath := c.imagePath(found)
	if imagePath == "" {
		raw, err := url.Parse(found.URLs.Raw)
		if err == nil && raw.Host == "plus.unsplash.com" {
			return nil, errPlus
		}
		return nil, fmt.Errorf("unexpected image link %q from Unsplash", found.URLs.Raw)
	}
	c.ping(ctx, found.Links.DownloadLocation)
	p, err := c.fetch(ctx, imagePath)
	if err != nil {
		return nil, err
	}
	p.ID = found.ID
	p.Filename = "unsplash-" + found.ID + ".jpg"
	if found.Links.HTML != "" {
		p.Page = found.Links.HTML + "?" + utm
	}
	if found.User.Name != "" {
		p.Attribution = "Photo by " + found.User.Name + " on Unsplash"
	}
	return p, nil
}

// imagePath is the path of a photo's image on images.unsplash.com, or "" for
// an Unsplash+ photo or an image anywhere else.
func (c *Client) imagePath(found apiPhoto) string {
	raw, err := url.Parse(found.URLs.Raw)
	if err != nil || raw.Host != c.images.Host || checkImagePath(raw.Path) != nil {
		return ""
	}
	return raw.Path
}

// apiPhoto is the part of an Unsplash API photo this package reads.
type apiPhoto struct {
	ID   string `json:"id"`
	URLs struct {
		Raw string `json:"raw"`
	} `json:"urls"`
	Links struct {
		HTML             string `json:"html"`
		DownloadLocation string `json:"download_location"`
	} `json:"links"`
	User struct {
		Name string `json:"name"`
	} `json:"user"`
}

// photoID finds the photo ID in the path of an Unsplash photo page:
// /photos/<slug> or /<language>/<word for photos>/<slug>, optionally with
// /download after it.
func photoID(p string) (string, bool) {
	segs := strings.FieldsFunc(p, func(r rune) bool { return r == '/' })
	if n := len(segs); n > 0 && segs[n-1] == "download" {
		segs = segs[:n-1]
	}
	switch {
	case len(segs) == 2 && (segs[0] == "photos" || segs[0] == "illustrations"):
	case len(segs) == 3 && len(segs[0]) >= 2 && len(segs[0]) <= 5:
	default:
		return "", false
	}
	return slugID(segs[len(segs)-1])
}

// slugID is the photo ID a page slug ends in: the slug is the ID, or words
// and "-" before it.
func slugID(slug string) (string, bool) {
	id := slug
	if len(slug) > idLen {
		if slug[len(slug)-idLen-1] != '-' {
			return "", false
		}
		id = slug[len(slug)-idLen:]
	}
	if id == "" || strings.ContainsFunc(id, func(r rune) bool { return !isIDRune(r) }) {
		return "", false
	}
	return id, true
}

// isIDRune reports whether r may be in a photo ID.
func isIDRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
}

// checkImagePath accepts the path of a free Unsplash image.
func checkImagePath(p string) error {
	switch {
	case strings.HasPrefix(p, "/premium_photo-"):
		return errPlus
	case !strings.HasPrefix(p, "/photo-"):
		return fmt.Errorf("%q is not an Unsplash photo", p)
	}
	return nil
}

// fetch reads the image at an images.unsplash.com path, Width pixels wide as a
// JPEG.
func (c *Client) fetch(ctx context.Context, imagePath string) (*Photo, error) {
	u := c.images.JoinPath(imagePath)
	u.RawQuery = url.Values{"w": {strconv.Itoa(Width)}, "q": {"80"}, "fm": {"jpg"}, "fit": {"max"}}.Encode()
	resp, err := c.get(ctx, u.String(), nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("got HTTP %d from Unsplash for the image %s", resp.StatusCode, imagePath)
	}
	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/") {
		return nil, fmt.Errorf("got %q from Unsplash for %s, not an image", contentType, imagePath)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPhotoBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read the image from Unsplash: %w", err)
	}
	if len(data) > maxPhotoBytes {
		return nil, fmt.Errorf("the image from Unsplash is larger than %d MB", maxPhotoBytes>>20)
	}
	return &Photo{Filename: path.Base(imagePath) + ".jpg", ContentType: contentType, Data: data}, nil
}

// apiGet reads an Unsplash API answer into out.
func (c *Client) apiGet(ctx context.Context, link string, out any) error {
	resp, err := c.get(ctx, link, c.apiHeaders())
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return errNotFound
	case http.StatusUnauthorized:
		return errors.New("the access key was refused by Unsplash (HTTP 401): check UNSPLASH_ACCESS_KEY")
	case http.StatusForbidden:
		return errors.New("this Unsplash access key has used up its hourly requests (HTTP 403): try again later")
	default:
		return fmt.Errorf("got HTTP %d from Unsplash", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJSONBytes)).Decode(out); err != nil {
		return fmt.Errorf("read Unsplash's answer: %w", err)
	}
	return nil
}

// ping tells Unsplash a photo was downloaded, as its API guidelines ask. It is a courtesy to the photographer's statistics, so a
// failure does not stop the background from being set.
func (c *Client) ping(ctx context.Context, link string) {
	u, err := url.Parse(link)
	if err != nil || u.Host != c.api.Host {
		return
	}
	if resp, err := c.get(ctx, u.String(), c.apiHeaders()); err == nil {
		_ = resp.Body.Close()
	}
}

func (c *Client) apiHeaders() http.Header {
	return http.Header{"Authorization": {"Client-ID " + c.key}, "Accept-Version": {"v1"}}
}

func (c *Client) get(ctx context.Context, link string, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, http.NoBody)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach Unsplash: %w", err)
	}
	return resp, nil
}
