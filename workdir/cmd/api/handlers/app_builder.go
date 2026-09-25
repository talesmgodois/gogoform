package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	apperrors "app/internal/errors"
)

// appBuilderPage is the data rendered by templates/builder.html.
type appBuilderPage struct {
	appLayout
	// Draft is the builderDraft to start from as JSON; empty for a blank form.
	Draft string
	// SourceTitle is the title of the form the draft was copied from, if any.
	SourceTitle string
}

// builderDraft is the state the form builder starts from: the parts of a
// FormRequest it edits.
type builderDraft struct {
	Title           string          `json:"title"`
	Slug            string          `json:"slug"`
	Description     *string         `json:"description"`
	Content         json.RawMessage `json:"content"`
	PublicAvailable bool            `json:"public_available"`
	AcceptAnonymous bool            `json:"accept_anonymous"`
}

// Builder renders the form builder. With ?form={id} it starts from a copy of
// that form; the copy gets a new slug since slugs are unique.
func (c *appController) Builder(w http.ResponseWriter, r *http.Request) {
	page := appBuilderPage{appLayout: appLayout{Active: "builder"}}
	if id := r.URL.Query().Get("form"); id != "" {
		f, err := c.form(r, "form", id)
		if err != nil {
			apperrors.WriteHTTP(w, r, err)
			return
		}
		draft, err := json.Marshal(builderDraft{
			Title:           f.Title,
			Slug:            copySlug(f.Slug),
			Description:     f.Description,
			Content:         f.Content,
			PublicAvailable: f.PublicAvailable,
			AcceptAnonymous: f.AcceptAnonymous,
		})
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
