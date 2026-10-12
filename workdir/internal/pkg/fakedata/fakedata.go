// Package fakedata serves the static sample collections embedded from
// fake.json (animes, cars, people and cities) for the public fake-data
// endpoints.
package fakedata

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

//go:embed fake.json
var fakeJSON []byte

// Collection names one of the embedded fake-data collections.
type Collection string

const (
	Animes Collection = "animes"
	Cars   Collection = "cars"
	People Collection = "people"
	Cities Collection = "cities"
)

// Record is a single row of a collection, keyed by its JSON field names.
type Record map[string]any

var collections map[Collection][]Record

func init() {
	var raw map[Collection][]Record
	if err := json.Unmarshal(fakeJSON, &raw); err != nil {
		panic(fmt.Errorf("fakedata: parse fake.json: %w", err))
	}
	collections = raw
}

// Names returns the name of every embedded collection.
func Names() []Collection {
	return []Collection{Animes, Cars, People, Cities}
}

// All returns every record of collection c, and whether c is known.
func All(c Collection) ([]Record, bool) {
	records, ok := collections[c]
	return records, ok
}

// reservedParams are query parameters consumed by pagination rather than
// field filtering.
var reservedParams = map[string]bool{"offset": true, "limit": true}

// Filter returns the records matching every non-pagination parameter in
// query. A record matches a parameter when the record's field, converted to
// a string, equals one of the parameter's values; a record missing the
// field does not match. Filters are combined with AND; multiple values for
// the same key are combined with OR.
func Filter(records []Record, query url.Values) []Record {
	filters := make(map[string][]string, len(query))
	for key, values := range query {
		if reservedParams[key] {
			continue
		}
		filters[key] = values
	}
	if len(filters) == 0 {
		return records
	}

	out := make([]Record, 0, len(records))
	for _, record := range records {
		if matches(record, filters) {
			out = append(out, record)
		}
	}
	return out
}

func matches(record Record, filters map[string][]string) bool {
	for key, values := range filters {
		field, ok := record[key]
		if !ok || !containsString(values, stringify(field)) {
			return false
		}
	}
	return true
}

func containsString(values []string, s string) bool {
	for _, v := range values {
		if v == s {
			return true
		}
	}
	return false
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}
