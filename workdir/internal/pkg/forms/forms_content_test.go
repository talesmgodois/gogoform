package forms

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// allTypesContent holds every field type, as the builder produces them, plus
// the layout-only types.
const allTypesContent = `{"version":1,"fields":[
	{"type":"textfield","name":"email","label":"E-mail","help":"Work address","required":true,"placeholder":"you@example.com","input_type":"email"},
	{"type":"textarea","name":"message","label":"Message","rows":6},
	{"type":"select","name":"plan","label":"Plan","options":[{"label":"Free","value":"free"},{"label":"Pro","value":"pro"}]},
	{"type":"select-data","name":"country","label":"Country","data_source":{"url":"/countries","label_key":"name","value_key":"id"}},
	{"type":"autocomplete-data","name":"city","label":"City","data_source":{"url":"https://example.com/cities","label_key":"name","value_key":"id","search_param":"q"},"min_chars":3},
	{"type":"file","name":"cv","label":"CV","accept":"image/*,.pdf","multiple":true,"max_size_mb":5},
	{"type":"toggle","name":"newsletter","label":"Newsletter","default":true},
	{"type":"panel","title":"Address","fields":[
		{"type":"textfield","name":"street","label":"Street"},
		{"type":"content","text":"We never share it."}
	]},
	{"type":"button","label":"Terms","url":"https://example.com/terms"}
]}`

func TestParseContentAllTypes(t *testing.T) {
	c, err := ParseContent(json.RawMessage(allTypesContent))
	if err != nil {
		t.Fatalf("ParseContent: %v", err)
	}
	if c.Version != 1 || len(c.Fields) != 9 {
		t.Fatalf("got version %d and %d fields", c.Version, len(c.Fields))
	}
	f := c.Fields
	if f[0].Type != FieldTextfield || f[0].Name != "email" || f[0].Label != "E-mail" || f[0].Help != "Work address" ||
		!f[0].Required || f[0].Placeholder != "you@example.com" || f[0].InputType != InputEmail {
		t.Errorf("textfield = %+v", f[0])
	}
	if f[1].Type != FieldTextarea || f[1].Rows != 6 {
		t.Errorf("textarea = %+v", f[1])
	}
	if f[2].Type != FieldSelect || len(f[2].Options) != 2 || f[2].Options[1] != (Option{Label: "Pro", Value: "pro"}) {
		t.Errorf("select = %+v", f[2])
	}
	if f[3].Type != FieldSelectData || *f[3].DataSource != (DataSource{URL: "/countries", LabelKey: "name", ValueKey: "id"}) {
		t.Errorf("select-data = %+v", f[3])
	}
	if f[4].Type != FieldAutocompleteData || f[4].DataSource.SearchParam != "q" || f[4].MinChars != 3 {
		t.Errorf("autocomplete-data = %+v", f[4])
	}
	if f[5].Type != FieldFile || f[5].Accept != "image/*,.pdf" || !f[5].Multiple || f[5].MaxSizeMB != 5 {
		t.Errorf("file = %+v", f[5])
	}
	if f[6].Type != FieldToggle || !f[6].Default {
		t.Errorf("toggle = %+v", f[6])
	}
	panel := f[7]
	if panel.Type != FieldPanel || panel.Title != "Address" || len(panel.Fields) != 2 ||
		panel.Fields[0].Name != "street" || panel.Fields[1].Type != FieldContent || panel.Fields[1].Text != "We never share it." {
		t.Errorf("panel = %+v", panel)
	}
	if f[8].Type != FieldButton || f[8].Label != "Terms" || f[8].URL != "https://example.com/terms" {
		t.Errorf("button = %+v", f[8])
	}
}

func TestParseContentDefaults(t *testing.T) {
	c, err := ParseContent(json.RawMessage(`{"fields":[
		{"type":"textfield","name":"a"},
		{"type":"file","name":"b"},
		{"type":"file","name":"c","max_size_mb":50}
	]}`))
	if err != nil {
		t.Fatalf("ParseContent: %v", err)
	}
	if c.Version != ContentVersion {
		t.Errorf("Version = %d, want %d", c.Version, ContentVersion)
	}
	if c.Fields[0].InputType != InputText {
		t.Errorf("InputType = %q, want %q", c.Fields[0].InputType, InputText)
	}
	if c.Fields[1].MaxSizeMB != MaxFileSizeMB || c.Fields[2].MaxSizeMB != MaxFileSizeMB {
		t.Errorf("MaxSizeMB = %v, %v, want %d", c.Fields[1].MaxSizeMB, c.Fields[2].MaxSizeMB, MaxFileSizeMB)
	}
}

func TestParseContentEmptyFields(t *testing.T) {
	c, err := ParseContent(json.RawMessage(`{"fields":[]}`))
	if err != nil || len(c.Fields) != 0 {
		t.Fatalf("ParseContent = %+v, %v", c, err)
	}
}

func TestParseContentRejects(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"empty", ``, "empty"},
		{"not an object", `[]`, "cannot unmarshal"},
		{"malformed", `{"fields":`, "unexpected end"},
		{"unsupported version", `{"version":2,"fields":[]}`, "unsupported version 2"},
		{"unknown type", `{"fields":[{"type":"signature","name":"s"}]}`, `unknown type "signature"`},
		{"missing type", `{"fields":[{"name":"s"}]}`, `unknown type ""`},
		{"unknown type in a panel", `{"fields":[{"type":"panel","fields":[{"type":"radio","name":"r"}]}]}`, `fields[0].fields[0]: unknown type "radio"`},
		{"missing name", `{"fields":[{"type":"textfield"}]}`, `invalid name ""`},
		{"invalid name", `{"fields":[{"type":"toggle","name":"1st"}]}`, `invalid name "1st"`},
		{"duplicate name", `{"fields":[{"type":"textfield","name":"a"},{"type":"textarea","name":"a"}]}`, `duplicate name "a"`},
		{"duplicate name across a panel", `{"fields":[{"type":"textfield","name":"a"},{"type":"panel","fields":[{"type":"toggle","name":"a"}]}]}`, `duplicate name "a"`},
		{"unknown input_type", `{"fields":[{"type":"textfield","name":"a","input_type":"color"}]}`, `unknown input_type "color"`},
		{"select without options", `{"fields":[{"type":"select","name":"a"}]}`, "at least one option"},
		{"duplicate option value", `{"fields":[{"type":"select","name":"a","options":[{"label":"X","value":"x"},{"label":"Y","value":"x"}]}]}`, `duplicate option value "x"`},
		{"select-data without source", `{"fields":[{"type":"select-data","name":"a"}]}`, "data_source.url"},
		{"autocomplete-data without URL", `{"fields":[{"type":"autocomplete-data","name":"a","data_source":{"label_key":"n"}}]}`, "data_source.url"},
		{"negative max_size_mb", `{"fields":[{"type":"file","name":"a","max_size_mb":-1}]}`, "max_size_mb"},
		{"nested fields outside a panel", `{"fields":[{"type":"content","fields":[{"type":"toggle","name":"a"}]}]}`, "only panels nest fields"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseContent(json.RawMessage(tt.content))
			if !errors.Is(err, ErrInvalidContent) {
				t.Fatalf("err = %v, want ErrInvalidContent", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestFieldTypeIsLayout(t *testing.T) {
	for _, typ := range []FieldType{FieldPanel, FieldContent, FieldButton} {
		if !typ.IsLayout() {
			t.Errorf("%s.IsLayout() = false", typ)
		}
	}
	for _, typ := range []FieldType{FieldTextfield, FieldTextarea, FieldSelect, FieldSelectData, FieldAutocompleteData, FieldFile, FieldToggle} {
		if typ.IsLayout() {
			t.Errorf("%s.IsLayout() = true", typ)
		}
	}
}

func TestContentField(t *testing.T) {
	c, err := ParseContent(json.RawMessage(allTypesContent))
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := c.Field("cv"); !ok || f.Type != FieldFile {
		t.Fatalf("cv = %+v, %v", f, ok)
	}
	if f, ok := c.Field("street"); !ok || f.Type != FieldTextfield {
		t.Fatalf("street (in panel) = %+v, %v", f, ok)
	}
	for _, name := range []string{"", "nope", "Address"} {
		if f, ok := c.Field(name); ok {
			t.Fatalf("Field(%q) = %+v, want none", name, f)
		}
	}
}

func TestFieldMaxSizeBytes(t *testing.T) {
	for _, tc := range []struct {
		mb   float64
		want int64
	}{{0, 10 << 20}, {5, 5 << 20}, {0.5, 512 << 10}, {50, 10 << 20}} {
		if got := (Field{Type: FieldFile, MaxSizeMB: tc.mb}).MaxSizeBytes(); got != tc.want {
			t.Errorf("MaxSizeBytes(%v) = %d, want %d", tc.mb, got, tc.want)
		}
	}
}

func TestFieldAccepts(t *testing.T) {
	for _, tc := range []struct {
		accept, name, contentType string
		want                      bool
	}{
		{"", "a.exe", "application/x-msdownload", true},
		{"image/*,.pdf", "photo.PNG", "image/png", true},
		{"image/*,.pdf", "cv.PDF", "application/octet-stream", true},
		{"image/*,.pdf", "notes.txt", "text/plain; charset=utf-8", false},
		{"IMAGE/*", "x", "image/jpeg", true},
		{"image/*", "x.png", "imagefoo/png", false},
		{"application/pdf", "x", "application/pdf; charset=binary", true},
		{"application/pdf", "x.pdf", "text/plain", false},
		{" .doc , .docx ", "letter.docx", "", true},
		{"text/plain", "x.txt", "not a type", false},
	} {
		f := Field{Type: FieldFile, Accept: tc.accept}
		if got := f.Accepts(tc.name, tc.contentType); got != tc.want {
			t.Errorf("Accept %q: Accepts(%q, %q) = %v, want %v", tc.accept, tc.name, tc.contentType, got, tc.want)
		}
	}
}
