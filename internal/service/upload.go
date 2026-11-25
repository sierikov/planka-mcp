package service

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// MaxUploadMB is the largest file Add uploads, in MB. The file is held in
// memory, so the request can be sent again after a re-login.
const MaxUploadMB = 50

const maxUpload = MaxUploadMB << 20

// localFile is a file read for an upload. Name is its base name as the path
// gave it, before any symlink was followed.
type localFile struct {
	name, contentType string
	data              []byte
}

// readUpload reads the file at path for an upload. The path must be absolute
// or start with "~/". A path with a hidden part, one whose name starts with
// ".", is refused before and after symlinks are followed: such files and
// folders hold keys and settings (~/.ssh, .env), and card text could ask for
// them. The file must be a regular file of at most maxUpload bytes.
func readUpload(path string) (localFile, error) {
	abs, err := absPath(path)
	if err != nil {
		return localFile{}, err
	}
	if err := notHidden(abs, path); err != nil {
		return localFile{}, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return localFile{}, fileError(path, err)
	}
	if err := notHidden(resolved, path); err != nil {
		return localFile{}, err
	}
	// Stat before opening: opening a named pipe would wait for a writer.
	st, err := os.Stat(resolved)
	if err != nil {
		return localFile{}, fileError(path, err)
	}
	if err := uploadable(path, st); err != nil {
		return localFile{}, err
	}
	f, err := os.Open(resolved)
	if err != nil {
		return localFile{}, fileError(path, err)
	}
	defer func() { _ = f.Close() }()
	if opened, err := f.Stat(); err != nil || !os.SameFile(st, opened) {
		return localFile{}, fmt.Errorf("%s changed while it was being read: try again", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxUpload+1))
	if err != nil {
		return localFile{}, fileError(path, err)
	}
	if len(data) > maxUpload {
		return localFile{}, tooLarge(path)
	}
	name := filepath.Base(abs)
	return localFile{name: name, contentType: mime.TypeByExtension(filepath.Ext(name)), data: data}, nil
}

// absPath expands a leading "~" to the home folder and cleans the path. A
// relative path is refused: the server's working folder is not one the
// caller knows.
func absPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot find the home folder for %s: %w", path, err)
		}
		path = filepath.Join(home, path[1:])
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path %q is not absolute: give the full path, or one that starts with ~/", path)
	}
	return filepath.Clean(path), nil
}

// notHidden refuses a path with a part whose name starts with ".". given is
// the path as the caller wrote it, for the error.
func notHidden(path, given string) error {
	for part := range strings.SplitSeq(filepath.ToSlash(path), "/") {
		if strings.HasPrefix(part, ".") {
			return fmt.Errorf("%s is or is inside a hidden file or folder (%s): those often hold keys and settings, so they are never uploaded", given, part)
		}
	}
	return nil
}

// uploadable checks that st is a regular file small enough to upload.
func uploadable(path string, st fs.FileInfo) error {
	switch {
	case st.IsDir():
		return fmt.Errorf("%s is a folder: attach the files in it one at a time", path)
	case !st.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file", path)
	case st.Size() > maxUpload:
		return tooLarge(path)
	}
	return nil
}

func tooLarge(path string) error {
	return fmt.Errorf("%s is larger than %d MB, the most this server uploads", path, MaxUploadMB)
}

// fileError words a file system error for the caller.
func fileError(path string, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("there is no file at %s", path)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("no permission to read %s", path)
	}
	return fmt.Errorf("cannot read %s: %w", path, err)
}

// checkLink checks a link is an http or https URL with a host, short enough
// for Planka.
func checkLink(link string) error {
	u, err := url.Parse(link)
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		return fmt.Errorf("url %q is not an http or https link", link)
	case len(link) > maxLinkURL:
		return fmt.Errorf("url is longer than %d characters", maxLinkURL)
	}
	return nil
}

// linkName is a link's default name: the link itself, or its host when the
// link is too long for a name.
func linkName(link string) string {
	if jsLen(link) <= maxAttachmentName {
		return link
	}
	if u, err := url.Parse(link); err == nil {
		return u.Host
	}
	return link
}
