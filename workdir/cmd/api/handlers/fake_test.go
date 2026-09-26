package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"app/cmd/api/handlers/fakedata"
)

func TestRecordMatches(t *testing.T) {
	rec := fakedata.Record{"name": "Civic", "year": 2020}
	tests := []struct {
		name    string
		filters map[string][]string
		want    bool
	}{
		{"no filters", nil, true},
		{"substring match, case-insensitive", map[string][]string{"name": {"civ"}}, true},
		{"no match", map[string][]string{"name": {"golf"}}, false},
		{"matches non-string field by string form", map[string][]string{"year": {"2020"}}, true},
		{"missing field", map[string][]string{"color": {"red"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := recordMatches(rec, tt.filters); got != tt.want {
				t.Errorf("recordMatches(%+v, %+v) = %v, want %v", rec, tt.filters, got, tt.want)
			}
		})
	}
}

func TestFakeCollectionHandler(t *testing.T) {
	records := []fakedata.Record{
		{"name": "Civic"},
		{"name": "Golf"},
	}
	handler := fakeCollectionHandler(records)

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/fake/cars", nil))
	assertStatus(t, rec, http.StatusOK)
	if got := decode[[]fakedata.Record](t, rec); len(got) != 2 {
		t.Fatalf("records = %+v, want 2", got)
	}

	filtered := httptest.NewRecorder()
	handler(filtered, httptest.NewRequest(http.MethodGet, "/fake/cars?name=civ", nil))
	assertStatus(t, filtered, http.StatusOK)
	if got := decode[[]fakedata.Record](t, filtered); len(got) != 1 || got[0]["name"] != "Civic" {
		t.Fatalf("records = %+v, want only Civic", got)
	}
}

func TestFakeEndpointsArePublic(t *testing.T) {
	for _, path := range []string{"/fake/animes", "/fake/cars", "/fake/people", "/fake/cities"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		Routes(testServices(), testAppConfig).ServeHTTP(rec, req)
		assertStatus(t, rec, http.StatusOK)
	}
}
