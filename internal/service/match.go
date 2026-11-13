package service

import (
	"net/url"
	"strings"
)

// normalize is the form names are compared in: trimmed and lower-cased.
func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// findMatches returns the indexes of the names that match query: the exact
// matches when there are any, otherwise the prefix matches. An empty query
// matches nothing.
func findMatches(query string, names []string) []int {
	q := normalize(query)
	if q == "" {
		return nil
	}
	var exact, prefix []int
	for i, n := range names {
		switch nn := normalize(n); {
		case nn == q:
			exact = append(exact, i)
		case strings.HasPrefix(nn, q):
			prefix = append(prefix, i)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return prefix
}

// matchName finds the one item whose name matches query. The match ignores
// case and surrounding space; an exact match beats a prefix match. No match is
// an UnknownNameError that lists every name, several equal matches an
// AmbiguousError. scope says where the items live, for example `board "X"`.
// id, when not nil, gives each candidate of an AmbiguousError its ID, so the
// caller can retry with it.
func matchName[T any](kind, query, scope string, items []T, name, id func(T) string) (T, error) {
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = name(it)
	}
	found := findMatches(query, names)
	switch len(found) {
	case 0:
		var zero T
		return zero, &UnknownNameError{Kind: kind, Name: query, Scope: scope, Valid: names}
	case 1:
		return items[found[0]], nil
	default:
		cands := make([]Candidate, len(found))
		for i, idx := range found {
			cands[i] = Candidate{Label: names[idx]}
			if id != nil {
				cands[i].ID = id(items[idx])
			}
		}
		var zero T
		return zero, &AmbiguousError{Kind: kind, Query: query, Candidates: cands}
	}
}

// byID finds the item whose ID is ref. Only a purely numeric ref, as Planka's
// IDs are, can match; it then wins over any name.
func byID[T any](ref string, items []T, id func(T) string) (T, bool) {
	ref = strings.TrimSpace(ref)
	if isDigits(ref) {
		for _, it := range items {
			if id(it) == ref {
				return it, true
			}
		}
	}
	var zero T
	return zero, false
}

// parseRef reads a Planka ID, or a Planka URL of the given kind ("cards" or
// "boards"), out of s. It reports false for anything else, such as a name.
func parseRef(s, kind string) (string, bool) {
	s = strings.TrimSpace(s)
	if isDigits(s) {
		return s, true
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}
	segs := strings.Split(u.Path, "/")
	for i := 0; i+1 < len(segs); i++ {
		if segs[i] == kind && isDigits(segs[i+1]) {
			return segs[i+1], true
		}
	}
	return "", false
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
