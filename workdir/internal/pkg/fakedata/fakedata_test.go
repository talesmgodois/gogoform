package fakedata

import (
	"net/url"
	"testing"
)

func TestAllKnownCollections(t *testing.T) {
	for _, c := range Names() {
		records, ok := All(c)
		if !ok {
			t.Fatalf("All(%s) ok = false, want true", c)
		}
		if len(records) == 0 {
			t.Fatalf("All(%s) returned no records", c)
		}
	}
}

func TestAllUnknownCollection(t *testing.T) {
	if _, ok := All("widgets"); ok {
		t.Fatalf("All(widgets) ok = true, want false")
	}
}

func TestFilterMatchesField(t *testing.T) {
	records, _ := All(Cars)

	query := url.Values{"branch": {"Toyota"}}
	got := Filter(records, query)

	if len(got) == 0 {
		t.Fatalf("Filter(branch=Toyota) returned no records")
	}
	for _, r := range got {
		if r["branch"] != "Toyota" {
			t.Fatalf("Filter(branch=Toyota) returned record with branch %v", r["branch"])
		}
	}
}

func TestFilterNoMatch(t *testing.T) {
	records, _ := All(Cars)

	query := url.Values{"branch": {"NoSuchBranch"}}
	got := Filter(records, query)

	if len(got) != 0 {
		t.Fatalf("Filter(branch=NoSuchBranch) returned %d records, want 0", len(got))
	}
}

func TestFilterIgnoresPaginationParams(t *testing.T) {
	records, _ := All(Cars)

	query := url.Values{"offset": {"0"}, "limit": {"10"}}
	got := Filter(records, query)

	if len(got) != len(records) {
		t.Fatalf("Filter with only pagination params returned %d records, want %d", len(got), len(records))
	}
}

func TestFilterUnknownField(t *testing.T) {
	records, _ := All(Cars)

	query := url.Values{"nope": {"anything"}}
	got := Filter(records, query)

	if len(got) != 0 {
		t.Fatalf("Filter(nope=anything) returned %d records, want 0", len(got))
	}
}

func TestFilterNumericField(t *testing.T) {
	records, _ := All(Cars)

	query := url.Values{"id": {"1"}}
	got := Filter(records, query)

	if len(got) != 1 {
		t.Fatalf("Filter(id=1) returned %d records, want 1", len(got))
	}
}
