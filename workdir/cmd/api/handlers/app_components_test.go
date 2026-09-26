package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"app/internal/pkg/customcomponents"
)

// postAppComponents requests POST /app/components with the dashboard
// credentials (none when user is empty) and the given body.
func postAppComponents(svc Services, user, pass, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/app/components", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	rec := httptest.NewRecorder()
	Routes(svc, testAppConfig).ServeHTTP(rec, req)
	return rec
}

// TestCreateComponentRequiresOperatorCredentials guards against the
// reusable-component endpoints being reachable only through a signed-in
// session: /app/builder, the only page that calls them, is an operator route
// guarded by HTTP Basic, not a session cookie.
func TestCreateComponentRequiresOperatorCredentials(t *testing.T) {
	rec := postAppComponents(testServices(), "", "", `{"name":"Agree","field":{"type":"checkbox"}}`)
	assertStatus(t, rec, http.StatusUnauthorized)
}

func TestCreateComponent(t *testing.T) {
	svc := testServices()
	rec := postAppComponents(svc, testAppConfig.Username, testAppConfig.Password, `{"name":"Agree","field":{"type":"checkbox"}}`)

	assertStatus(t, rec, http.StatusCreated)
	got := svc.customComponents.(*fakeCustomComponents).created
	if got.Name != "Agree" || string(got.Field) != `{"type":"checkbox"}` {
		t.Fatalf("created = %+v", got)
	}
	if got.UserID != nil {
		t.Fatalf("UserID = %v, want nil: the operator-credentials path has no signed-in user", got.UserID)
	}
}

func TestCreateComponentValidatesInput(t *testing.T) {
	tests := map[string]string{
		"missing name":  `{"field":{"type":"checkbox"}}`,
		"blank name":    `{"name":"  ","field":{"type":"checkbox"}}`,
		"missing field": `{"name":"Agree"}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			rec := postAppComponents(testServices(), testAppConfig.Username, testAppConfig.Password, body)
			assertStatus(t, rec, http.StatusBadRequest)
		})
	}
}

func TestListComponents(t *testing.T) {
	userID := int32(3)
	svc := testServices()
	svc.customComponents.(*fakeCustomComponents).list = []customcomponents.CustomComponent{
		{ID: 1, Name: "Agree", Field: json.RawMessage(`{"type":"checkbox"}`), UserID: &userID},
	}

	rec := getAppPath(svc, "/app/components")

	assertStatus(t, rec, http.StatusOK)
	got := decode[[]appComponentResponse](t, rec)
	if len(got) != 1 || got[0].Name != "Agree" || got[0].UserID == nil || *got[0].UserID != userID {
		t.Fatalf("components = %+v", got)
	}
}

func TestListComponentsRequiresOperatorCredentials(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/app/components", nil)
	rec := httptest.NewRecorder()
	Routes(testServices(), testAppConfig).ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusUnauthorized)
}
