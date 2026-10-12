package handlers

import (
	"net/http"
	"testing"
)

func TestFakeCarsList(t *testing.T) {
	rec := do(t, testServices(), http.MethodGet, "/fake/cars", "", false)
	assertStatus(t, rec, http.StatusOK)
	body := decode[CarListResponse](t, rec)
	if body.Total != len(fakeData.Cars) || len(body.Items) != len(fakeData.Cars) {
		t.Fatalf("total = %d, items = %d, want %d", body.Total, len(body.Items), len(fakeData.Cars))
	}
}

func TestFakeCarsFilterByID(t *testing.T) {
	rec := do(t, testServices(), http.MethodGet, "/fake/cars?id=1", "", false)
	assertStatus(t, rec, http.StatusOK)
	body := decode[CarListResponse](t, rec)
	if body.Total != 1 || body.Items[0].Name != "Corolla" {
		t.Fatalf("unexpected filter result: %+v", body)
	}
}

func TestFakeCarsFilterByNameCaseInsensitive(t *testing.T) {
	rec := do(t, testServices(), http.MethodGet, "/fake/cars?name=corol", "", false)
	assertStatus(t, rec, http.StatusOK)
	body := decode[CarListResponse](t, rec)
	if body.Total != 1 || body.Items[0].Branch != "Toyota" {
		t.Fatalf("unexpected filter result: %+v", body)
	}
}

func TestFakeCarsInvalidID(t *testing.T) {
	rec := do(t, testServices(), http.MethodGet, "/fake/cars?id=nope", "", false)
	assertStatus(t, rec, http.StatusBadRequest)
}

func TestFakeCarsPagination(t *testing.T) {
	rec := do(t, testServices(), http.MethodGet, "/fake/cars?offset=0&limit=5", "", false)
	assertStatus(t, rec, http.StatusOK)
	body := decode[CarListResponse](t, rec)
	if len(body.Items) != 5 || body.Total != len(fakeData.Cars) {
		t.Fatalf("unexpected page: %+v", body)
	}
}

func TestFakePeopleFilterByEmail(t *testing.T) {
	rec := do(t, testServices(), http.MethodGet, "/fake/people?email=ana.silva", "", false)
	assertStatus(t, rec, http.StatusOK)
	body := decode[PersonListResponse](t, rec)
	if body.Total != 1 || body.Items[0].Name != "Ana Silva" {
		t.Fatalf("unexpected filter result: %+v", body)
	}
}

func TestFakeAnimesFilterByDescription(t *testing.T) {
	rec := do(t, testServices(), http.MethodGet, "/fake/animes?description=Titans", "", false)
	assertStatus(t, rec, http.StatusOK)
	body := decode[AnimeListResponse](t, rec)
	if body.Total != 1 || body.Items[0].Name != "Attack on Titan" {
		t.Fatalf("unexpected filter result: %+v", body)
	}
}

func TestFakeCitiesFilterByCountryCode(t *testing.T) {
	rec := do(t, testServices(), http.MethodGet, "/fake/cities?country_code=br", "", false)
	assertStatus(t, rec, http.StatusOK)
	body := decode[CityListResponse](t, rec)
	if body.Total != 2 {
		t.Fatalf("total = %d, want 2 (Goiânia and São Paulo)", body.Total)
	}
}

func TestFakeEndpointsNoAuthRequired(t *testing.T) {
	for _, path := range []string{"/fake/cars", "/fake/people", "/fake/animes", "/fake/cities"} {
		rec := do(t, testServices(), http.MethodGet, path, "", false)
		assertStatus(t, rec, http.StatusOK)
	}
}
