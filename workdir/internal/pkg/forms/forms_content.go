package forms

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"path"
	"regexp"
	"strings"
)

// ContentVersion is the only form content version understood by ParseContent.
const ContentVersion = 1

// FieldType is the type of a field of the form content.
type FieldType string

// Input field types, as produced by the builder. Each one yields a value in
// the submission payload under the field's Name.
const (
	FieldTextfield        FieldType = "textfield"
	FieldTextarea         FieldType = "textarea"
	FieldSelect           FieldType = "select"
	FieldSelectData       FieldType = "select-data"
	FieldAutocompleteData FieldType = "autocomplete-data"
	FieldFile             FieldType = "file"
	FieldToggle           FieldType = "toggle"
)

// Layout-only field types: they shape the page and never yield a value.
const (
	// FieldPanel groups its nested Fields under a Title.
	FieldPanel FieldType = "panel"
	// FieldContent shows static Text.
	FieldContent FieldType = "content"
	// FieldButton is a non-submitting button, linking to URL when set.
	FieldButton FieldType = "button"
)

// IsLayout reports whether t is a layout-only type, which never yields a
// value in the submission payload.
func (t FieldType) IsLayout() bool {
	return t == FieldPanel || t == FieldContent || t == FieldButton
}

func (t FieldType) known() bool {
	switch t {
	case FieldTextfield, FieldTextarea, FieldSelect, FieldSelectData,
		FieldAutocompleteData, FieldFile, FieldToggle:
		return true
	}
	return t.IsLayout()
}

// Input types of a textfield.
const (
	InputText   = "text"
	InputEmail  = "email"
	InputNumber = "number"
	InputTel    = "tel"
	InputURL    = "url"
	InputDate   = "date"
)

var inputTypes = map[string]bool{
	InputText: true, InputEmail: true, InputNumber: true,
	InputTel: true, InputURL: true, InputDate: true,
}

// MaxFileSizeMB is the upload limit of a file field, and its default size.
const MaxFileSizeMB = 10

// fieldNameRe matches the names the builder accepts (see NAME_RE).
var fieldNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ErrInvalidContent is wrapped by every error returned by ParseContent.
var ErrInvalidContent = errors.New("invalid form content")

// Content is the typed form definition stored in Form.Content.
type Content struct {
	Version int     `json:"version"`
	Fields  []Field `json:"fields"`
}

// Field is one component of the form content. Which properties apply depends
// on Type; the others are left empty.
type Field struct {
	Type     FieldType `json:"type"`
	Name     string    `json:"name,omitempty"`
	Label    string    `json:"label,omitempty"`
	Help     string    `json:"help,omitempty"`
	Required bool      `json:"required,omitempty"`
	// Placeholder applies to textfield, textarea, select, select-data and
	// autocomplete-data.
	Placeholder string `json:"placeholder,omitempty"`

	// InputType is the textfield kind, one of the Input* constants; empty
	// means InputText.
	InputType string `json:"input_type,omitempty"`
	// Rows is the textarea height.
	Rows int `json:"rows,omitempty"`
	// Options are the allowed values of a select.
	Options []Option `json:"options,omitempty"`
	// DataSource feeds select-data and autocomplete-data.
	DataSource *DataSource `json:"data_source,omitempty"`
	// MinChars is the autocomplete-data search threshold.
	MinChars int `json:"min_chars,omitempty"`
	// Accept is the comma-separated list of MIME types or extensions a file
	// field takes; empty accepts anything.
	Accept string `json:"accept,omitempty"`
	// Multiple file fields take several files.
	Multiple bool `json:"multiple,omitempty"`
	// MaxSizeMB is the per-file limit of a file field; ParseContent turns
	// zero, or anything above MaxFileSizeMB, into MaxFileSizeMB.
	MaxSizeMB float64 `json:"max_size_mb,omitempty"`
	// Default is the initial state of a toggle.
	Default bool `json:"default,omitempty"`

	// Title heads a panel.
	Title string `json:"title,omitempty"`
	// Fields are the components nested in a panel.
	Fields []Field `json:"fields,omitempty"`
	// Text is the static text of a content component.
	Text string `json:"text,omitempty"`
	// URL is where a button links to; empty renders an inert button.
	URL string `json:"url,omitempty"`
}

// Option is one allowed value of a select.
type Option struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// DataSource is the URL options or suggestions are loaded from.
type DataSource struct {
	URL      string `json:"url"`
	LabelKey string `json:"label_key,omitempty"`
	ValueKey string `json:"value_key,omitempty"`
	// SearchParam is the query parameter autocomplete-data sends its term in.
	SearchParam string `json:"search_param,omitempty"`
}

// ParseContent decodes and checks a form definition: a missing version means
// ContentVersion, every field has a known type, input fields have a valid
// name unique across the whole form (panels included), textfields a known
// input_type, selects at least one option, data fields a URL and file fields
// a non-negative max_size_mb, capped at MaxFileSizeMB. Errors wrap
// ErrInvalidContent.
func ParseContent(raw json.RawMessage) (Content, error) {
	var c Content
	if len(bytes.TrimSpace(raw)) == 0 {
		return c, fmt.Errorf("%w: empty", ErrInvalidContent)
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return Content{}, fmt.Errorf("%w: %v", ErrInvalidContent, err)
	}
	switch c.Version {
	case 0:
		c.Version = ContentVersion
	case ContentVersion:
	default:
		return Content{}, fmt.Errorf("%w: unsupported version %d", ErrInvalidContent, c.Version)
	}
	names := map[string]bool{}
	if err := checkFields(c.Fields, "fields", names); err != nil {
		return Content{}, fmt.Errorf("%w: %v", ErrInvalidContent, err)
	}
	return c, nil
}

func checkFields(fields []Field, path string, names map[string]bool) error {
	for i := range fields {
		f := &fields[i]
		where := fmt.Sprintf("%s[%d]", path, i)
		if !f.Type.known() {
			return fmt.Errorf("%s: unknown type %q", where, f.Type)
		}
		if f.Type == FieldPanel {
			if err := checkFields(f.Fields, where+".fields", names); err != nil {
				return err
			}
			continue
		}
		if len(f.Fields) > 0 {
			return fmt.Errorf("%s: only panels nest fields", where)
		}
		if f.Type.IsLayout() {
			continue
		}
		if !fieldNameRe.MatchString(f.Name) {
			return fmt.Errorf("%s: invalid name %q", where, f.Name)
		}
		if names[f.Name] {
			return fmt.Errorf("%s: duplicate name %q", where, f.Name)
		}
		names[f.Name] = true
		if err := checkField(f); err != nil {
			return fmt.Errorf("%s (%s): %v", where, f.Name, err)
		}
	}
	return nil
}

// checkField checks and normalizes the type-specific properties of an input
// field.
func checkField(f *Field) error {
	switch f.Type {
	case FieldTextfield:
		if f.InputType == "" {
			f.InputType = InputText
		}
		if !inputTypes[f.InputType] {
			return fmt.Errorf("unknown input_type %q", f.InputType)
		}
	case FieldSelect:
		if len(f.Options) == 0 {
			return errors.New("select needs at least one option")
		}
		seen := map[string]bool{}
		for _, o := range f.Options {
			if seen[o.Value] {
				return fmt.Errorf("duplicate option value %q", o.Value)
			}
			seen[o.Value] = true
		}
	case FieldSelectData, FieldAutocompleteData:
		if f.DataSource == nil || f.DataSource.URL == "" {
			return errors.New("data_source.url is required")
		}
	case FieldFile:
		switch {
		case f.MaxSizeMB < 0:
			return errors.New("max_size_mb must not be negative")
		case f.MaxSizeMB == 0 || f.MaxSizeMB > MaxFileSizeMB:
			f.MaxSizeMB = MaxFileSizeMB
		}
	}
	return nil
}

// Field returns the input field called name, looking inside panels too.
// Layout components have no name and are never returned.
func (c Content) Field(name string) (Field, bool) {
	return findField(c.Fields, name)
}

func findField(fields []Field, name string) (Field, bool) {
	for _, f := range fields {
		if f.Type == FieldPanel {
			if found, ok := findField(f.Fields, name); ok {
				return found, true
			}
			continue
		}
		if !f.Type.IsLayout() && f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// MaxSizeBytes is the per-file limit of a file field, in bytes (MaxSizeMB
// is counted in MiB).
func (f Field) MaxSizeBytes() int64 {
	mb := f.MaxSizeMB
	if mb <= 0 || mb > MaxFileSizeMB {
		mb = MaxFileSizeMB
	}
	return int64(mb * (1 << 20))
}

// Accepts reports whether a file field takes a file called fileName of the
// given content type, following the HTML accept attribute: each
// comma-separated entry is an extension (".pdf"), a MIME type
// ("application/pdf") or a MIME type wildcard ("image/*"), compared
// case-insensitively. An empty Accept takes anything.
func (f Field) Accepts(fileName, contentType string) bool {
	if strings.TrimSpace(f.Accept) == "" {
		return true
	}
	ext := strings.ToLower(path.Ext(fileName))
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = ""
	}
	mediaType = strings.ToLower(mediaType)
	for _, entry := range strings.Split(f.Accept, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		switch {
		case entry == "":
		case strings.HasPrefix(entry, "."):
			if ext == entry {
				return true
			}
		case strings.HasSuffix(entry, "/*"):
			if mediaType != "" && strings.HasPrefix(mediaType, strings.TrimSuffix(entry, "*")) {
				return true
			}
		case mediaType == entry:
			return true
		}
	}
	return false
}
