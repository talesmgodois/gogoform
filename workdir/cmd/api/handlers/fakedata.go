package handlers

import (
	"net/http"

	apperrors "app/internal/errors"
	"app/internal/pkg/fakedata"
)

// FakeDataListResponse is a page of records from one of the embedded
// fake-data collections.
type FakeDataListResponse struct {
	Items []fakedata.Record `json:"items"`
	Total int64             `json:"total" example:"20"`
	PageResponse
}

// FakeAnimes lists the embedded animes collection.
//
//	@Summary		List fake animes
//	@Description	Returns a page of the embedded sample animes collection. Any query parameter other than offset/limit filters records whose matching field equals one of the given values; repeat a parameter to OR its values.
//	@Tags			fake-data
//	@Produce		json
//	@Param			offset	query		int							false	"Number of records to skip"			minimum(0)	default(0)
//	@Param			limit	query		int							false	"Maximum number of records to return"	minimum(1)	maximum(100)	default(20)
//	@Success		200		{object}	FakeDataListResponse		"Page of animes"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid query parameters"
//	@Router			/fake/animes [get]
func FakeAnimes(w http.ResponseWriter, r *http.Request) {
	fakeDataList(w, r, fakedata.Animes)
}

// FakeCars lists the embedded cars collection.
//
//	@Summary		List fake cars
//	@Description	Returns a page of the embedded sample cars collection. Any query parameter other than offset/limit filters records whose matching field equals one of the given values; repeat a parameter to OR its values.
//	@Tags			fake-data
//	@Produce		json
//	@Param			offset	query		int							false	"Number of records to skip"			minimum(0)	default(0)
//	@Param			limit	query		int							false	"Maximum number of records to return"	minimum(1)	maximum(100)	default(20)
//	@Success		200		{object}	FakeDataListResponse		"Page of cars"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid query parameters"
//	@Router			/fake/cars [get]
func FakeCars(w http.ResponseWriter, r *http.Request) {
	fakeDataList(w, r, fakedata.Cars)
}

// FakePeople lists the embedded people collection.
//
//	@Summary		List fake people
//	@Description	Returns a page of the embedded sample people collection. Any query parameter other than offset/limit filters records whose matching field equals one of the given values; repeat a parameter to OR its values.
//	@Tags			fake-data
//	@Produce		json
//	@Param			offset	query		int							false	"Number of records to skip"			minimum(0)	default(0)
//	@Param			limit	query		int							false	"Maximum number of records to return"	minimum(1)	maximum(100)	default(20)
//	@Success		200		{object}	FakeDataListResponse		"Page of people"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid query parameters"
//	@Router			/fake/people [get]
func FakePeople(w http.ResponseWriter, r *http.Request) {
	fakeDataList(w, r, fakedata.People)
}

// FakeCities lists the embedded cities collection.
//
//	@Summary		List fake cities
//	@Description	Returns a page of the embedded sample cities collection. Any query parameter other than offset/limit filters records whose matching field equals one of the given values; repeat a parameter to OR its values.
//	@Tags			fake-data
//	@Produce		json
//	@Param			offset	query		int							false	"Number of records to skip"			minimum(0)	default(0)
//	@Param			limit	query		int							false	"Maximum number of records to return"	minimum(1)	maximum(100)	default(20)
//	@Success		200		{object}	FakeDataListResponse		"Page of cities"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid query parameters"
//	@Router			/fake/cities [get]
func FakeCities(w http.ResponseWriter, r *http.Request) {
	fakeDataList(w, r, fakedata.Cities)
}

// fakeDataList writes a filtered, paginated page of collection.
func fakeDataList(w http.ResponseWriter, r *http.Request, collection fakedata.Collection) {
	offset, limit, err := parsePage(r)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	records, _ := fakedata.All(collection)
	filtered := fakedata.Filter(records, r.URL.Query())
	writeJSON(w, r, http.StatusOK, FakeDataListResponse{
		Items:        paginateRecords(filtered, offset, limit),
		Total:        int64(len(filtered)),
		PageResponse: PageResponse{Offset: offset, Limit: limit},
	})
}

// paginateRecords returns the slice of records starting at offset, up to
// limit items.
func paginateRecords(records []fakedata.Record, offset, limit int32) []fakedata.Record {
	if offset < 0 || int(offset) >= len(records) {
		return []fakedata.Record{}
	}
	end := int(offset) + int(limit)
	if end > len(records) {
		end = len(records)
	}
	return records[offset:end]
}
