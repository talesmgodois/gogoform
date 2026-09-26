package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"app/cmd/api/handlers/fakedata"
)

// fakeCollectionHandler returns a handler serving records, filtered by every
// query parameter as a case-insensitive substring match against the
// same-named field. Records missing a filtered field, or whose value does
// not contain the filter substring, are excluded.
func fakeCollectionHandler(records []fakedata.Record) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filters := r.URL.Query()
		matches := make([]fakedata.Record, 0, len(records))
		for _, rec := range records {
			if recordMatches(rec, filters) {
				matches = append(matches, rec)
			}
		}
		writeJSON(w, r, http.StatusOK, matches)
	}
}

// recordMatches reports whether rec has every field named in filters,
// containing the filter's value as a case-insensitive substring.
func recordMatches(rec fakedata.Record, filters map[string][]string) bool {
	for field, values := range filters {
		v, ok := rec[field]
		if !ok {
			return false
		}
		want := strings.ToLower(values[0])
		if !strings.Contains(strings.ToLower(fmt.Sprint(v)), want) {
			return false
		}
	}
	return true
}

// FakeAnimes returns the fake animes collection, filterable by any field.
//
//	@Summary		Fake animes
//	@Description	Demo fixture data; filter by any field via query params (case-insensitive substring match).
//	@Tags			fake
//	@Produce		json
//	@Success		200	{array}	object	"Matching records"
//	@Router			/fake/animes [get]
func FakeAnimes(w http.ResponseWriter, r *http.Request) {
	fakeCollectionHandler(fakedata.Animes)(w, r)
}

// FakeCars returns the fake cars collection, filterable by any field.
//
//	@Summary		Fake cars
//	@Description	Demo fixture data; filter by any field via query params (case-insensitive substring match).
//	@Tags			fake
//	@Produce		json
//	@Success		200	{array}	object	"Matching records"
//	@Router			/fake/cars [get]
func FakeCars(w http.ResponseWriter, r *http.Request) {
	fakeCollectionHandler(fakedata.Cars)(w, r)
}

// FakePeople returns the fake people collection, filterable by any field.
//
//	@Summary		Fake people
//	@Description	Demo fixture data; filter by any field via query params (case-insensitive substring match).
//	@Tags			fake
//	@Produce		json
//	@Success		200	{array}	object	"Matching records"
//	@Router			/fake/people [get]
func FakePeople(w http.ResponseWriter, r *http.Request) {
	fakeCollectionHandler(fakedata.People)(w, r)
}

// FakeCities returns the fake cities collection, filterable by any field.
//
//	@Summary		Fake cities
//	@Description	Demo fixture data; filter by any field via query params (case-insensitive substring match).
//	@Tags			fake
//	@Produce		json
//	@Success		200	{array}	object	"Matching records"
//	@Router			/fake/cities [get]
func FakeCities(w http.ResponseWriter, r *http.Request) {
	fakeCollectionHandler(fakedata.Cities)(w, r)
}
