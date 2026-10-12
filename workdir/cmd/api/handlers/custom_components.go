package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	apperrors "app/internal/errors"
	"app/internal/pkg/customcomponents"
)

// maxComponentNameLen matches the custom_components.name column size.
const maxComponentNameLen = 255

// CreateCustomComponentRequest is the body of POST /custom-components.
type CreateCustomComponentRequest struct {
	Name string `json:"name" example:"Car picker"`
	// FieldJSON is the field definition to save, in the same shape used
	// inside a form's content.
	FieldJSON json.RawMessage `json:"field_json" swaggertype:"object"`
}

// CustomComponentResponse is a saved reusable component as returned by the API.
type CustomComponentResponse struct {
	ID        int32           `json:"id" example:"1"`
	UserID    int32           `json:"user_id" example:"1"`
	Name      string          `json:"name" example:"Car picker"`
	FieldJSON json.RawMessage `json:"field_json" swaggertype:"object"`
	CreatedAt time.Time       `json:"created_at" example:"2026-01-01T00:00:00Z"`
}

// CustomComponentListResponse is the list of every saved component.
type CustomComponentListResponse struct {
	Items []CustomComponentResponse `json:"items"`
}

// customComponentsController serves the reusable component endpoints.
type customComponentsController struct {
	customComponents customcomponents.Repository
}

// Create saves a field definition as a reusable component.
//
//	@Summary		Save a reusable component
//	@Description	Saves a field definition from the form builder so it can be reinserted into other forms. Requires a signed-in user.
//	@Tags			custom-components
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Param			body	body		CreateCustomComponentRequest	true	"Component to save"
//	@Success		201		{object}	CustomComponentResponse		"Component saved"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid request"
//	@Failure		401		{object}	errors.HTTPErrorResponse	"Not signed in"
//	@Failure		500		{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/custom-components [post]
func (c *customComponentsController) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateCustomComponentRequest
	if err := decodeJSON(w, r, &req); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	if req.Name == "" || len(req.Name) > maxComponentNameLen {
		apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("name must be between 1 and 255 characters"))
		return
	}
	if len(req.FieldJSON) == 0 || bytes.Equal(req.FieldJSON, []byte("null")) {
		apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("field_json must not be empty"))
		return
	}
	comp, err := c.customComponents.Create(r.Context(), customcomponents.CreateCustomComponentInput{
		UserID:    userFrom(r.Context()).ID,
		Name:      req.Name,
		FieldJSON: req.FieldJSON,
	})
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusCreated, toCustomComponentResponse(comp))
}

// List lists every saved reusable component, across every user.
//
//	@Summary		List reusable components
//	@Description	Lists every field definition saved from the form builder, newest first. Requires a signed-in user.
//	@Tags			custom-components
//	@Produce		json
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Success		200	{object}	CustomComponentListResponse	"Saved components"
//	@Failure		401	{object}	errors.HTTPErrorResponse	"Not signed in"
//	@Failure		500	{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/custom-components [get]
func (c *customComponentsController) List(w http.ResponseWriter, r *http.Request) {
	components, err := c.customComponents.List(r.Context())
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	resp := CustomComponentListResponse{Items: make([]CustomComponentResponse, len(components))}
	for i, comp := range components {
		resp.Items[i] = toCustomComponentResponse(comp)
	}
	writeJSON(w, r, http.StatusOK, resp)
}

func toCustomComponentResponse(comp customcomponents.CustomComponent) CustomComponentResponse {
	return CustomComponentResponse{
		ID:        comp.ID,
		UserID:    comp.UserID,
		Name:      comp.Name,
		FieldJSON: comp.FieldJSON,
		CreatedAt: comp.CreatedAt,
	}
}
