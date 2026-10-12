package handlers

import (
	"embed"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	apperrors "app/internal/errors"
)

//go:embed fakedata/fake.json
var fakeDataFile embed.FS

// CarResponse is a car in the fake /fake/cars dataset.
type CarResponse struct {
	ID     int32  `json:"id" example:"1"`
	Name   string `json:"name" example:"Corolla"`
	Branch string `json:"branch" example:"Toyota"`
}

// PersonResponse is a person in the fake /fake/people dataset.
type PersonResponse struct {
	Email string `json:"email" example:"ana.silva@example.com"`
	CPF   string `json:"cpf" example:"123.456.789-01"`
	Name  string `json:"name" example:"Ana Silva"`
}

// AnimeResponse is an anime in the fake /fake/animes dataset.
type AnimeResponse struct {
	ID          int32  `json:"id" example:"1"`
	Name        string `json:"name" example:"Attack on Titan"`
	Description string `json:"description" example:"Humanity fights for survival against giant humanoid creatures known as Titans."`
}

// CityResponse is a city in the fake /fake/cities dataset.
type CityResponse struct {
	ID          int32  `json:"id" example:"1"`
	Name        string `json:"name" example:"Goiânia"`
	CountryCode string `json:"country_code" example:"BR"`
}

// CarListResponse is a page of cars.
type CarListResponse struct {
	Items []CarResponse `json:"items"`
	Total int           `json:"total" example:"20"`
	PageResponse
}

// PersonListResponse is a page of people.
type PersonListResponse struct {
	Items []PersonResponse `json:"items"`
	Total int              `json:"total" example:"20"`
	PageResponse
}

// AnimeListResponse is a page of animes.
type AnimeListResponse struct {
	Items []AnimeResponse `json:"items"`
	Total int             `json:"total" example:"20"`
	PageResponse
}

// CityListResponse is a page of cities.
type CityListResponse struct {
	Items []CityResponse `json:"items"`
	Total int            `json:"total" example:"20"`
	PageResponse
}

// fakeDataset is the shape of the embedded fakedata/fake.json file, which
// holds the four collections served under /fake.
type fakeDataset struct {
	Cars   []CarResponse    `json:"cars"`
	People []PersonResponse `json:"people"`
	Animes []AnimeResponse  `json:"animes"`
	Cities []CityResponse   `json:"cities"`
}

// fakeData is loaded once from the embedded fakedata/fake.json.
var fakeData fakeDataset

func init() {
	b, err := fakeDataFile.ReadFile("fakedata/fake.json")
	if err != nil {
		panic("handlers: read embedded fakedata/fake.json: " + err.Error())
	}
	if err := json.Unmarshal(b, &fakeData); err != nil {
		panic("handlers: parse embedded fakedata/fake.json: " + err.Error())
	}
}

// fakeDataController serves static, in-memory data for prototyping
// data-bound components (select, autocomplete) without a real backend.
type fakeDataController struct{}

// Cars lists the fake cars dataset.
//
//	@Summary		List fake cars
//	@Description	Returns cars from the static fake dataset. Every field can be used as a filter: id matches exactly, name and branch match a case-insensitive substring.
//	@Tags			cars
//	@Produce		json
//	@Param			id		query		int							false	"Exact car ID"
//	@Param			name	query		string						false	"Case-insensitive substring of the car name"
//	@Param			branch	query		string						false	"Case-insensitive substring of the car branch"
//	@Param			offset	query		int							false	"Number of cars to skip"				minimum(0)	default(0)
//	@Param			limit	query		int							false	"Maximum number of cars to return"		minimum(1)	maximum(100)	default(20)
//	@Success		200		{object}	CarListResponse				"Page of cars"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid query parameters"
//	@Router			/fake/cars [get]
func (c *fakeDataController) Cars(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := parsePage(r)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	q := r.URL.Query()
	items := fakeData.Cars
	if v := strings.TrimSpace(q.Get("id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("id must be an integer"))
			return
		}
		items = filterFake(items, func(it CarResponse) bool { return int64(it.ID) == id })
	}
	if v := strings.TrimSpace(q.Get("name")); v != "" {
		items = filterFake(items, func(it CarResponse) bool { return containsFold(it.Name, v) })
	}
	if v := strings.TrimSpace(q.Get("branch")); v != "" {
		items = filterFake(items, func(it CarResponse) bool { return containsFold(it.Branch, v) })
	}
	writeJSON(w, r, http.StatusOK, CarListResponse{
		Items:        paginateFake(items, offset, limit),
		Total:        len(items),
		PageResponse: PageResponse{Offset: offset, Limit: limit},
	})
}

// People lists the fake people dataset.
//
//	@Summary		List fake people
//	@Description	Returns people from the static fake dataset. Every field can be used as a filter, matching a case-insensitive substring.
//	@Tags			people
//	@Produce		json
//	@Param			email	query		string						false	"Case-insensitive substring of the email"
//	@Param			cpf		query		string						false	"Case-insensitive substring of the CPF"
//	@Param			name	query		string						false	"Case-insensitive substring of the name"
//	@Param			offset	query		int							false	"Number of people to skip"			minimum(0)	default(0)
//	@Param			limit	query		int							false	"Maximum number of people to return"	minimum(1)	maximum(100)	default(20)
//	@Success		200		{object}	PersonListResponse			"Page of people"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid query parameters"
//	@Router			/fake/people [get]
func (c *fakeDataController) People(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := parsePage(r)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	q := r.URL.Query()
	items := fakeData.People
	if v := strings.TrimSpace(q.Get("email")); v != "" {
		items = filterFake(items, func(it PersonResponse) bool { return containsFold(it.Email, v) })
	}
	if v := strings.TrimSpace(q.Get("cpf")); v != "" {
		items = filterFake(items, func(it PersonResponse) bool { return containsFold(it.CPF, v) })
	}
	if v := strings.TrimSpace(q.Get("name")); v != "" {
		items = filterFake(items, func(it PersonResponse) bool { return containsFold(it.Name, v) })
	}
	writeJSON(w, r, http.StatusOK, PersonListResponse{
		Items:        paginateFake(items, offset, limit),
		Total:        len(items),
		PageResponse: PageResponse{Offset: offset, Limit: limit},
	})
}

// Animes lists the fake animes dataset.
//
//	@Summary		List fake animes
//	@Description	Returns animes from the static fake dataset. Every field can be used as a filter: id matches exactly, name and description match a case-insensitive substring.
//	@Tags			animes
//	@Produce		json
//	@Param			id			query		int							false	"Exact anime ID"
//	@Param			name		query		string						false	"Case-insensitive substring of the anime name"
//	@Param			description	query		string						false	"Case-insensitive substring of the description"
//	@Param			offset		query		int							false	"Number of animes to skip"			minimum(0)	default(0)
//	@Param			limit		query		int							false	"Maximum number of animes to return"	minimum(1)	maximum(100)	default(20)
//	@Success		200			{object}	AnimeListResponse			"Page of animes"
//	@Failure		400			{object}	errors.HTTPErrorResponse	"Invalid query parameters"
//	@Router			/fake/animes [get]
func (c *fakeDataController) Animes(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := parsePage(r)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	q := r.URL.Query()
	items := fakeData.Animes
	if v := strings.TrimSpace(q.Get("id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("id must be an integer"))
			return
		}
		items = filterFake(items, func(it AnimeResponse) bool { return int64(it.ID) == id })
	}
	if v := strings.TrimSpace(q.Get("name")); v != "" {
		items = filterFake(items, func(it AnimeResponse) bool { return containsFold(it.Name, v) })
	}
	if v := strings.TrimSpace(q.Get("description")); v != "" {
		items = filterFake(items, func(it AnimeResponse) bool { return containsFold(it.Description, v) })
	}
	writeJSON(w, r, http.StatusOK, AnimeListResponse{
		Items:        paginateFake(items, offset, limit),
		Total:        len(items),
		PageResponse: PageResponse{Offset: offset, Limit: limit},
	})
}

// Cities lists the fake cities dataset.
//
//	@Summary		List fake cities
//	@Description	Returns cities from the static fake dataset. Every field can be used as a filter: id matches exactly, name and country_code match a case-insensitive substring.
//	@Tags			cities
//	@Produce		json
//	@Param			id				query		int							false	"Exact city ID"
//	@Param			name			query		string						false	"Case-insensitive substring of the city name"
//	@Param			country_code	query		string						false	"Case-insensitive substring of the country code"
//	@Param			offset			query		int							false	"Number of cities to skip"			minimum(0)	default(0)
//	@Param			limit			query		int							false	"Maximum number of cities to return"	minimum(1)	maximum(100)	default(20)
//	@Success		200				{object}	CityListResponse			"Page of cities"
//	@Failure		400				{object}	errors.HTTPErrorResponse	"Invalid query parameters"
//	@Router			/fake/cities [get]
func (c *fakeDataController) Cities(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := parsePage(r)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	q := r.URL.Query()
	items := fakeData.Cities
	if v := strings.TrimSpace(q.Get("id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("id must be an integer"))
			return
		}
		items = filterFake(items, func(it CityResponse) bool { return int64(it.ID) == id })
	}
	if v := strings.TrimSpace(q.Get("name")); v != "" {
		items = filterFake(items, func(it CityResponse) bool { return containsFold(it.Name, v) })
	}
	if v := strings.TrimSpace(q.Get("country_code")); v != "" {
		items = filterFake(items, func(it CityResponse) bool { return containsFold(it.CountryCode, v) })
	}
	writeJSON(w, r, http.StatusOK, CityListResponse{
		Items:        paginateFake(items, offset, limit),
		Total:        len(items),
		PageResponse: PageResponse{Offset: offset, Limit: limit},
	})
}

// containsFold reports whether s contains substr, ignoring case.
func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// filterFake returns the items of a fake dataset for which keep is true.
func filterFake[T any](items []T, keep func(T) bool) []T {
	out := make([]T, 0, len(items))
	for _, it := range items {
		if keep(it) {
			out = append(out, it)
		}
	}
	return out
}

// paginateFake returns the [offset, offset+limit) slice of items, which
// must already be filtered. An offset beyond the end of items yields an
// empty (non-nil) slice.
func paginateFake[T any](items []T, offset, limit int32) []T {
	start := int(offset)
	if start >= len(items) {
		return []T{}
	}
	end := start + int(limit)
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}
