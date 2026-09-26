package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	apperrors "app/internal/errors"
	"app/internal/pkg/customcomponents"
)

// appComponentRequest is the body of POST /app/components.
type appComponentRequest struct {
	Name  string          `json:"name"`
	Field json.RawMessage `json:"field"`
}

// appComponentResponse is a saved custom component, as JSON.
type appComponentResponse struct {
	ID        int32           `json:"id"`
	Name      string          `json:"name"`
	Field     json.RawMessage `json:"field"`
	UserID    *int32          `json:"user_id"`
	CreatedAt time.Time       `json:"created_at"`
}

func toComponentResponse(c customcomponents.CustomComponent) appComponentResponse {
	return appComponentResponse{ID: c.ID, Name: c.Name, Field: c.Field, UserID: c.UserID, CreatedAt: c.CreatedAt}
}

// CreateComponent saves the field definition of req as a reusable component
// owned by the signed-in user.
func (c *appController) CreateComponent(w http.ResponseWriter, r *http.Request) {
	var req appComponentRequest
	if err := decodeJSON(w, r, &req); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("name is required"))
		return
	}
	if len(req.Field) == 0 {
		apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("field is required"))
		return
	}
	input := customcomponents.CreateCustomComponentInput{Name: req.Name, Field: req.Field}
	// The builder is reached through operator credentials, not a user
	// session (see routes.go), so there usually is no signed-in user to
	// attribute the component to.
	if u := userFrom(r.Context()); u != nil {
		input.UserID = &u.ID
	}
	comp, err := c.customComponents.Create(r.Context(), input)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusCreated, toComponentResponse(comp))
}

// ListComponents returns every saved custom component, newest first.
func (c *appController) ListComponents(w http.ResponseWriter, r *http.Request) {
	items, err := c.customComponents.List(r.Context())
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	resp := make([]appComponentResponse, len(items))
	for i, item := range items {
		resp[i] = toComponentResponse(item)
	}
	writeJSON(w, r, http.StatusOK, resp)
}
