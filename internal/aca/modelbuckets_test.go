package aca_test

import (
	"slices"
	"testing"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/aca/acatest"
)

// bucketsCase is a model by id, name or both, and the named buckets ModelBuckets
// must give it; known false for one it must not know.
type bucketsCase struct {
	id, name string
	buckets  []string
	known    bool
}

func checkModelBuckets(t *testing.T, a aca.AiCodingAgent, cases []bucketsCase) {
	t.Helper()
	for _, c := range cases {
		buckets, known := a.ModelBuckets(c.id, c.name)
		if known != c.known || !slices.Equal(buckets, c.buckets) {
			t.Errorf("%s ModelBuckets(%q, %q) = %q, %v; want %q, %v", a.ID(), c.id, c.name, buckets, known, c.buckets, c.known)
		}
	}
}

// unknownModels is what no ACA knows, by id, by name and by both.
var unknownModels = []bucketsCase{
	{},
	{id: "llama-9-70b"},
	{name: "Llama 9 70B"},
	{id: "llama-9-70b", name: "Llama 9 70B"},
	{name: "rm -rf /"},
}

// knownByIDAndName checks each model of models is known by its id alone, by its name
// alone and by both, with the buckets want gives it.
func knownByIDAndName(models []aca.Model, want func(aca.Model) []string) []bucketsCase {
	var cases []bucketsCase
	for _, m := range models {
		buckets := want(m)
		cases = append(cases,
			bucketsCase{id: m.ID, buckets: buckets, known: true},
			bucketsCase{name: m.Name, buckets: buckets, known: true},
			bucketsCase{id: m.ID, name: m.Name, buckets: buckets, known: true})
	}
	return cases
}

func TestNoModelBucketsKnowsNone(t *testing.T) {
	t.Parallel()
	bare := &acatest.Bare{Name: "bare"}
	checkModelBuckets(t, bare, append(slices.Clone(unknownModels), bucketsCase{id: "opus"}, bucketsCase{name: "Opus 5.5"}))
}

// The test ACA maps what the test gives it, the id first.
func TestTestACAModelBuckets(t *testing.T) {
	t.Parallel()
	a := &acatest.ACA{Buckets: map[string][]string{"m-1": {"b-1"}, "Model One": {"b-1"}, "m-2": nil, "Model Two": {"b-2"}}}
	checkModelBuckets(t, a, append(slices.Clone(unknownModels),
		bucketsCase{id: "m-1", buckets: []string{"b-1"}, known: true},
		bucketsCase{name: "Model One", buckets: []string{"b-1"}, known: true},
		bucketsCase{id: "m-2", name: "Model One", known: true},
		bucketsCase{id: "m-9", name: "Model Two", buckets: []string{"b-2"}, known: true},
	))
	if buckets, known := (&acatest.ACA{}).ModelBuckets("m-1", "Model One"); known || buckets != nil {
		t.Errorf("with no Buckets: %q, %v; want nothing known", buckets, known)
	}
}
