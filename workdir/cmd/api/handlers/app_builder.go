package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	apperrors "app/internal/errors"
)

// Builder modes, recorded on the page for its script.
const (
	builderModeNew  = "new"
	builderModeEdit = "edit"
	builderModeCopy = "copy"
)

// appBuilderPage is the data rendered by templates/builder.html.
type appBuilderPage struct {
	appLayout
	// Mode is builderModeNew, builderModeEdit or builderModeCopy.
	Mode string
	// FormID is the ID of the form being edited; zero unless Mode is
	// builderModeEdit.
	FormID int32
	// Draft is the builderDraft to start from as JSON; empty for a blank form.
	Draft string
	// SourceTitle is the title of the form being edited or copied, if any.
	SourceTitle string
}

// builderDraft is the state the form builder starts from: the parts of a
// FormRequest it edits. IsActive and the dates are only set when editing, so
// saving keeps them; a copy starts with the defaults.
type builderDraft struct {
	Title           string          `json:"title"`
	Slug            string          `json:"slug"`
	Description     *string         `json:"description"`
	IsActive        *bool           `json:"is_active,omitempty"`
	StartDate       *time.Time      `json:"start_date,omitempty"`
	EndDate         *time.Time      `json:"end_date,omitempty"`
	Content         json.RawMessage `json:"content"`
	PublicAvailable bool            `json:"public_available"`
	AcceptAnonymous bool            `json:"accept_anonymous"`
}

// Builder renders the form builder. With ?form={id} it edits that form of
// the viewer's tenant, which is saved with PUT /forms/{id}. With ?copy={id}
// it starts from a copy of that form, which gets a new slug since slugs are
// unique. Otherwise it starts from a blank form; new forms are created with
// POST /forms.
func (c *appController) Builder(w http.ResponseWriter, r *http.Request) {
	page := appBuilderPage{appLayout: appLayout{Active: "dashboard"}, Mode: builderModeNew}
	name, id := "form", r.URL.Query().Get("form")
	if id != "" {
		page.Mode = builderModeEdit
	} else if id = r.URL.Query().Get("copy"); id != "" {
		name, page.Mode = "copy", builderModeCopy
	}
	if page.Mode != builderModeNew {
		f, err := c.form(r, name, id)
		if err != nil {
			renderAppError(w, r, err)
			return
		}
		d := builderDraft{
			Title:           f.Title,
			Slug:            copySlug(f.Slug),
			Description:     f.Description,
			Content:         f.Content,
			PublicAvailable: f.PublicAvailable,
			AcceptAnonymous: f.AcceptAnonymous,
		}
		if page.Mode == builderModeEdit {
			d.Slug, d.IsActive, d.StartDate, d.EndDate = f.Slug, &f.IsActive, f.StartDate, f.EndDate
			page.FormID = f.ID
		}
		draft, err := json.Marshal(d)
		if err != nil {
			apperrors.WriteHTTP(w, r, apperrors.NewInternal(err))
			return
		}
		page.Draft, page.SourceTitle = string(draft), f.Title
	}
	renderAppPage(w, r, appBuilderTemplate, &page)
}

// copySlug derives the slug of a copy of the form with the given slug,
// keeping it valid and within maxFormTextLen.
func copySlug(slug string) string {
	const suffix = "-copy"
	if len(slug)+len(suffix) > maxFormTextLen {
		slug = strings.TrimRight(slug[:maxFormTextLen-len(suffix)], "-")
	}
	return slug + suffix
}
