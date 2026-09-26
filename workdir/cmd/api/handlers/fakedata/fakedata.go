// Package fakedata serves the fixtures embedded from fake.json as in-memory
// collections, backing the /fake/* demo endpoints.
package fakedata

import (
	"embed"
	"encoding/json"
)

//go:embed fake.json
var fakeFS embed.FS

// Record is a single row of a fake collection, keyed by field name.
type Record = map[string]any

// collections mirrors the top-level shape of fake.json.
type collections struct {
	Cars   []Record `json:"cars"`
	People []Record `json:"people"`
	Animes []Record `json:"animes"`
	Cities []Record `json:"cities"`
}

var fake = mustLoadFake()

// Cars, People, Animes and Cities are the fixture rows for the matching
// /fake/* collection.
var (
	Cars   = fake.Cars
	People = fake.People
	Animes = fake.Animes
	Cities = fake.Cities
)

// mustLoadFake decodes fake.json from the embedded filesystem, panicking if
// the embedded fixture is missing or malformed.
func mustLoadFake() collections {
	data, err := fakeFS.ReadFile("fake.json")
	if err != nil {
		panic(err)
	}
	var v collections
	if err := json.Unmarshal(data, &v); err != nil {
		panic(err)
	}
	return v
}
