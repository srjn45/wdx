package aca

import (
	"slices"
	"strings"
)

// modelFamily is a row of an ACA's ModelBuckets table: the models of one family, by
// id and by name, and the named buckets they draw on.
type modelFamily struct {
	ids        []string // ids it is, whole
	idPrefixes []string // what its other ids start with
	names      []string // names it is, whole
	nameWords  []string // a word one of its names has, alone or before a "-", in any case
	buckets    []string
}

// modelBuckets is ModelBuckets on table: the first row the id matches, else the
// first the name matches, else not known.
func modelBuckets(table []modelFamily, id, name string) ([]string, bool) {
	if id != "" {
		for _, f := range table {
			if slices.Contains(f.ids, id) || slices.ContainsFunc(f.idPrefixes, func(p string) bool { return strings.HasPrefix(id, p) }) {
				return slices.Clone(f.buckets), true
			}
		}
	}
	if name != "" {
		for _, f := range table {
			if slices.Contains(f.names, name) || slices.ContainsFunc(f.nameWords, func(w string) bool { return hasWord(name, w) }) {
				return slices.Clone(f.buckets), true
			}
		}
	}
	return nil, false
}

// hasWord says whether a word of name is word, or starts with it and a "-", as
// GPT-OSS does GPT, in any case.
func hasWord(name, word string) bool {
	for _, field := range strings.Fields(name) {
		head, _, _ := strings.Cut(field, "-")
		if strings.EqualFold(head, word) {
			return true
		}
	}
	return false
}
