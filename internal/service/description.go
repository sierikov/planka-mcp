package service

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// A card description is read and edited by section. Sections are opened by
// markdown headings and by bold-lead lines (a line that is only a bold phrase,
// such as **Folders**), which some cards use instead of headings. A section is
// named by its position in the outline: "1", "1.2", "1.2.1". Text before the
// first heading is section "0". Positions change when headings are added or
// removed, so every read carries a DescriptionVersion, and an edit can insist
// on the version it was planned against.

// OutlineEntry is one section of a description. Chars counts its characters,
// subsections included.
type OutlineEntry struct {
	ID, Heading string
	Level       int
	Chars       int
}

// section is a part of a description: text[start:end], of which
// text[start:bodyStart] is the heading line.
type section struct {
	OutlineEntry
	start, bodyStart, end int
}

var (
	atxHeading = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$`)
	boldLead   = regexp.MustCompile(`^ {0,3}(?:\*\*([^*\n]+)\*\*|__([^_\n]+)__):?[ \t]*$`)
	fenceOpen  = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
)

// DescriptionVersion names a description's text: the first 12 hex digits of
// its SHA-256. An empty description has the version "".
func DescriptionVersion(text string) string {
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:12]
}

// Outline lists a description's sections in order.
func Outline(text string) []OutlineEntry {
	secs := sections(text)
	out := make([]OutlineEntry, len(secs))
	for i, s := range secs {
		out[i] = s.OutlineEntry
	}
	return out
}

// Section returns the text of the section id, heading included.
func Section(text, id string) (string, error) {
	s, err := findSection(sections(text), id)
	if err != nil {
		return "", err
	}
	return text[s.start:s.end], nil
}

// heading is a heading line found in a description.
type heading struct {
	level, start, bodyStart int
	text                    string
}

// headings finds the heading lines of text outside fenced code. A bold-lead
// line is one level below the heading above it, or level 1 with none above.
func headings(text string) []heading {
	var out []heading
	fence := ""
	level := 0 // of the last ATX heading
	for start := 0; start < len(text); {
		end := strings.IndexByte(text[start:], '\n')
		next := len(text)
		if end >= 0 {
			next = start + end + 1
		}
		line := strings.TrimRight(text[start:next], "\r\n")
		switch {
		case fence != "":
			if strings.HasPrefix(strings.TrimLeft(line, " "), fence) {
				fence = ""
			}
		case fenceOpen.MatchString(line):
			fence = fenceOpen.FindStringSubmatch(line)[1]
		default:
			if m := atxHeading.FindStringSubmatch(line); m != nil {
				level = len(m[1])
				out = append(out, heading{level: level, start: start, bodyStart: next, text: strings.TrimSpace(m[2])})
			} else if m := boldLead.FindStringSubmatch(line); m != nil {
				text := strings.TrimSuffix(strings.TrimSpace(m[1]+m[2]), ":")
				out = append(out, heading{level: level + 1, start: start, bodyStart: next, text: text})
			}
		}
		start = next
	}
	return out
}

// sections splits text into its sections, numbered by position.
func sections(text string) []section {
	hs := headings(text)
	var out []section
	firstStart := len(text)
	if len(hs) > 0 {
		firstStart = hs[0].start
	}
	if strings.TrimSpace(text[:firstStart]) != "" {
		out = append(out, section{OutlineEntry: OutlineEntry{ID: "0"}, start: 0, bodyStart: 0, end: firstStart})
	}
	type open struct {
		level, children int
		id              string
	}
	stack := []open{{level: 0}}
	for i, h := range hs {
		for len(stack) > 1 && stack[len(stack)-1].level >= h.level {
			stack = stack[:len(stack)-1]
		}
		parent := &stack[len(stack)-1]
		parent.children++
		id := strconv.Itoa(parent.children)
		if parent.id != "" {
			id = parent.id + "." + id
		}
		stack = append(stack, open{level: h.level, id: id})
		end := len(text)
		for _, later := range hs[i+1:] {
			if later.level <= h.level {
				end = later.start
				break
			}
		}
		out = append(out, section{
			OutlineEntry: OutlineEntry{ID: id, Heading: h.text, Level: h.level},
			start:        h.start, bodyStart: h.bodyStart, end: end,
		})
	}
	for i := range out {
		out[i].Chars = utf8.RuneCountInString(text[out[i].start:out[i].end])
	}
	return out
}

// findSection finds a section by its outline ID. An unknown ID is an error
// that lists the outline.
func findSection(secs []section, id string) (section, error) {
	id = strings.TrimSpace(id)
	if i := slices.IndexFunc(secs, func(s section) bool { return s.ID == id }); i >= 0 {
		return secs[i], nil
	}
	if len(secs) == 0 {
		return section{}, fmt.Errorf("no section %q: the description has no sections", id)
	}
	names := make([]string, len(secs))
	for i, s := range secs {
		names[i] = s.ID + " " + cmp.Or(s.Heading, "(intro)")
	}
	return section{}, fmt.Errorf("no section %q; sections: %s", id, strings.Join(names, ", "))
}

// DescriptionEdit is one change to a description, exactly one of:
//   - Body: replace the text under Section's heading (subsections included);
//   - Append: add text at the end of Section, or of the whole description
//     when Section is empty;
//   - Find and Replace: replace the one occurrence of Find, in Section or,
//     when Section is empty, in the whole description.
type DescriptionEdit struct {
	Section                     string
	Body, Append, Find, Replace *string
}

// change replaces text[start:end] with with.
type change struct {
	start, end int
	with       string
	edit       int // index of the edit, for errors
}

// applyEdits applies edits to text, every one found in text as it is before
// any applies. It refuses a find that is missing or not unique, and edits
// that overlap. It returns the new text and the changes made, in order.
func applyEdits(text string, edits []DescriptionEdit) (string, []change, error) {
	if len(edits) == 0 {
		return "", nil, errors.New("edits is empty: give at least one edit")
	}
	secs := sections(text)
	changes := make([]change, 0, len(edits))
	for i, e := range edits {
		c, err := planEdit(text, secs, e)
		if err != nil {
			return "", nil, fmt.Errorf("edit %d: %w", i+1, err)
		}
		c.edit = i
		changes = append(changes, c)
	}
	slices.SortStableFunc(changes, func(a, b change) int { return a.start - b.start })
	for i := 1; i < len(changes); i++ {
		a, b := changes[i-1], changes[i]
		if b.start < a.end || (b.start == a.start && a.start == a.end) {
			return "", nil, fmt.Errorf("edits %d and %d change the same text: combine them into one", a.edit+1, b.edit+1)
		}
	}
	var out strings.Builder
	last := 0
	for _, c := range changes {
		out.WriteString(text[last:c.start])
		out.WriteString(c.with)
		last = c.end
	}
	out.WriteString(text[last:])
	return out.String(), changes, nil
}

// planEdit turns one edit into the change it makes to text.
func planEdit(text string, secs []section, e DescriptionEdit) (change, error) {
	if err := checkEdit(e); err != nil {
		return change{}, err
	}
	scope := section{end: len(text)}
	if strings.TrimSpace(e.Section) != "" {
		var err error
		if scope, err = findSection(secs, e.Section); err != nil {
			return change{}, err
		}
	}
	switch {
	case e.Find != nil:
		return findChange(text, scope, *e.Find, *e.Replace)
	case e.Body != nil:
		return bodyChange(text, scope, *e.Body), nil
	default:
		return appendChange(text, scope, *e.Append)
	}
}

// checkEdit checks that an edit is exactly one of its kinds, complete.
func checkEdit(e DescriptionEdit) error {
	kinds := 0
	for _, set := range []bool{e.Body != nil, e.Append != nil, e.Find != nil} {
		if set {
			kinds++
		}
	}
	switch {
	case kinds != 1:
		return errors.New("give exactly one of body, append and find")
	case e.Find != nil && e.Replace == nil:
		return errors.New("find needs replace (it may be empty)")
	case e.Find == nil && e.Replace != nil:
		return errors.New("replace needs find")
	case e.Body != nil && strings.TrimSpace(e.Section) == "":
		return errors.New("body needs section: to replace the whole description, use update_card")
	}
	return nil
}

// bodyChange replaces the text under scope's heading with body, keeping the
// line breaks that ended it.
func bodyChange(text string, scope section, body string) change {
	trail := trailingNewlines(text[scope.bodyStart:scope.end])
	body = strings.TrimRight(body, "\n")
	if body != "" && scope.end < len(text) && trail == "" {
		trail = "\n"
	}
	if body != "" && scope.bodyStart > 0 && text[scope.bodyStart-1] != '\n' {
		body = "\n" + body // the heading is the last line, without a line break
	}
	return change{start: scope.bodyStart, end: scope.end, with: body + trail}
}

// appendChange adds lines at the end of scope, before the line breaks that
// end it.
func appendChange(text string, scope section, add string) (change, error) {
	add = strings.Trim(add, "\n")
	if add == "" {
		return change{}, errors.New("append is empty")
	}
	at := scope.end - len(trailingNewlines(text[scope.bodyStart:scope.end]))
	if at > scope.bodyStart {
		return change{start: at, end: at, with: "\n" + add}, nil
	}
	// Nothing under the heading yet.
	if scope.bodyStart > 0 && text[scope.bodyStart-1] != '\n' {
		return change{start: scope.bodyStart, end: scope.bodyStart, with: "\n" + add}, nil
	}
	return change{start: scope.bodyStart, end: scope.bodyStart, with: add + "\n"}, nil
}

// findChange replaces the one occurrence of find in text[scope.start:scope.end].
func findChange(text string, scope section, find, replace string) (change, error) {
	if find == "" {
		return change{}, errors.New("find is empty")
	}
	area := text[scope.start:scope.end]
	where := "the description"
	if scope.ID != "" {
		where = "section " + scope.ID
	}
	switch n := strings.Count(area, find); n {
	case 0:
		return change{}, fmt.Errorf("find %q does not occur in %s", clip(find), where)
	case 1:
		i := scope.start + strings.Index(area, find)
		return change{start: i, end: i + len(find), with: replace}, nil
	default:
		return change{}, fmt.Errorf("find %q occurs %d times in %s: add text around it so it occurs once", clip(find), n, where)
	}
}

// trailingNewlines is the run of line breaks that ends s.
func trailingNewlines(s string) string {
	return s[len(strings.TrimRight(s, "\r\n")):]
}

// clip shortens s for an error message.
func clip(s string) string {
	const limit = 60
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit]) + "…"
}
