package forms

import (
	"encoding/json"
	"reflect"
	"testing"
)

const fileA = "0b6a3b2e-8f1c-4c4e-9d5e-2f1a7c3b9e10"
const fileB = "1c7b4c3f-9a2d-4d5f-8e6f-3a2b8d4c0f21"

// validationContent holds one optional field per type (and per textfield
// input_type) plus required ones, a panel and layout components.
const validationContent = `{"version":1,"fields":[
	{"type":"textfield","name":"text"},
	{"type":"textfield","name":"email","input_type":"email"},
	{"type":"textfield","name":"number","input_type":"number"},
	{"type":"textfield","name":"tel","input_type":"tel"},
	{"type":"textfield","name":"url","input_type":"url"},
	{"type":"textfield","name":"date","input_type":"date"},
	{"type":"textarea","name":"message"},
	{"type":"select","name":"plan","options":[{"label":"Free","value":"free"},{"label":"Pro","value":"pro"}]},
	{"type":"select-data","name":"country","data_source":{"url":"/countries"}},
	{"type":"autocomplete-data","name":"city","data_source":{"url":"/cities"}},
	{"type":"file","name":"photo"},
	{"type":"file","name":"docs","multiple":true},
	{"type":"toggle","name":"newsletter"},
	{"type":"panel","title":"Required","fields":[
		{"type":"textfield","name":"req_text","required":true},
		{"type":"textfield","name":"req_number","input_type":"number","required":true},
		{"type":"select","name":"req_plan","required":true,"options":[{"label":"Free","value":"free"}]},
		{"type":"select-data","name":"req_country","required":true,"data_source":{"url":"/countries"}},
		{"type":"file","name":"req_photo","required":true},
		{"type":"file","name":"req_docs","required":true,"multiple":true},
		{"type":"toggle","name":"req_terms","required":true},
		{"type":"content","name":"notice","text":"Read the terms."}
	]},
	{"type":"button","name":"terms","label":"Terms","url":"https://example.com/terms"}
]}`

// validRequired answers every required field.
var validRequired = map[string]string{
	"req_text":    `"Ada"`,
	"req_number":  `42`,
	"req_plan":    `"free"`,
	"req_country": `"br"`,
	"req_photo":   `"` + fileA + `"`,
	"req_docs":    `["` + fileA + `"]`,
	"req_terms":   `true`,
}

func payloadWith(extra map[string]string) map[string]json.RawMessage {
	p := map[string]json.RawMessage{}
	for k, v := range validRequired {
		p[k] = json.RawMessage(v)
	}
	for k, v := range extra {
		if v == "" {
			delete(p, k)
			continue
		}
		p[k] = json.RawMessage(v)
	}
	return p
}

func TestValidatePayload(t *testing.T) {
	c, err := ParseContent(json.RawMessage(validationContent))
	if err != nil {
		t.Fatalf("ParseContent: %v", err)
	}
	tests := []struct {
		name string
		// extra sets payload keys on top of validRequired; "" removes one.
		extra map[string]string
		want  map[string]string
	}{
		{name: "required only", extra: nil, want: nil},
		{name: "null optional values", extra: map[string]string{
			"text": `null`, "number": `null`, "plan": `null`, "photo": `null`, "docs": `null`, "newsletter": `null`,
		}, want: nil},

		// unknown and layout keys
		{name: "unknown key", extra: map[string]string{"nope": `"x"`}, want: map[string]string{"nope": MsgUnknownField}},
		{name: "button key", extra: map[string]string{"terms": `true`}, want: map[string]string{"terms": MsgUnknownField}},
		{name: "content key", extra: map[string]string{"notice": `"x"`}, want: map[string]string{"notice": MsgUnknownField}},

		// required
		{name: "required missing", extra: map[string]string{"req_text": "", "req_plan": "", "req_photo": ""},
			want: map[string]string{"req_text": MsgRequired, "req_plan": MsgRequired, "req_photo": MsgRequired}},
		{name: "required null", extra: map[string]string{"req_text": `null`}, want: map[string]string{"req_text": MsgRequired}},
		{name: "required blank string", extra: map[string]string{"req_text": `"  "`}, want: map[string]string{"req_text": MsgRequired}},
		{name: "required blank number", extra: map[string]string{"req_number": `""`}, want: map[string]string{"req_number": MsgRequired}},
		{name: "required blank data", extra: map[string]string{"req_country": `""`}, want: map[string]string{"req_country": MsgRequired}},
		{name: "required empty file", extra: map[string]string{"req_photo": `""`}, want: map[string]string{"req_photo": MsgRequired}},
		{name: "required empty files", extra: map[string]string{"req_docs": `[]`}, want: map[string]string{"req_docs": MsgRequired}},
		{name: "required toggle false", extra: map[string]string{"req_terms": `false`}, want: map[string]string{"req_terms": MsgRequired}},
		{name: "everything missing", extra: map[string]string{
			"req_text": "", "req_number": "", "req_plan": "", "req_country": "", "req_photo": "", "req_docs": "", "req_terms": "",
		}, want: map[string]string{
			"req_text": MsgRequired, "req_number": MsgRequired, "req_plan": MsgRequired, "req_country": MsgRequired,
			"req_photo": MsgRequired, "req_docs": MsgRequired, "req_terms": MsgRequired,
		}},

		// textfield text and textarea
		{name: "text string", extra: map[string]string{"text": `"hello"`, "message": `"line 1\nline 2"`}, want: nil},
		{name: "text empty optional", extra: map[string]string{"text": `""`}, want: nil},
		{name: "text number", extra: map[string]string{"text": `1`}, want: map[string]string{"text": MsgNotString}},
		{name: "textarea object", extra: map[string]string{"message": `{"a":1}`}, want: map[string]string{"message": MsgNotString}},
		{name: "textarea array", extra: map[string]string{"message": `["a"]`}, want: map[string]string{"message": MsgNotString}},

		// textfield email
		{name: "email valid", extra: map[string]string{"email": `"ada@example.com"`}, want: nil},
		{name: "email invalid", extra: map[string]string{"email": `"ada.example.com"`}, want: map[string]string{"email": MsgInvalidEmail}},
		{name: "email display name", extra: map[string]string{"email": `"Ada <ada@example.com>"`}, want: map[string]string{"email": MsgInvalidEmail}},
		{name: "email not string", extra: map[string]string{"email": `true`}, want: map[string]string{"email": MsgNotString}},

		// textfield number
		{name: "number json", extra: map[string]string{"number": `-3.5`}, want: nil},
		{name: "number string", extra: map[string]string{"number": `"12"`}, want: nil},
		{name: "number empty optional", extra: map[string]string{"number": `""`}, want: nil},
		{name: "number text", extra: map[string]string{"number": `"twelve"`}, want: map[string]string{"number": MsgInvalidNumber}},
		{name: "number infinite", extra: map[string]string{"number": `"Inf"`}, want: map[string]string{"number": MsgInvalidNumber}},
		{name: "number bool", extra: map[string]string{"number": `true`}, want: map[string]string{"number": MsgInvalidNumber}},

		// textfield tel
		{name: "tel valid", extra: map[string]string{"tel": `"+55 (11) 91234-5678"`}, want: nil},
		{name: "tel letters", extra: map[string]string{"tel": `"call me"`}, want: map[string]string{"tel": MsgInvalidTel}},
		{name: "tel too short", extra: map[string]string{"tel": `"+1"`}, want: map[string]string{"tel": MsgInvalidTel}},

		// textfield url
		{name: "url valid", extra: map[string]string{"url": `"https://example.com/a?b=c"`}, want: nil},
		{name: "url no scheme", extra: map[string]string{"url": `"example.com"`}, want: map[string]string{"url": MsgInvalidURL}},
		{name: "url javascript", extra: map[string]string{"url": `"javascript:alert(1)"`}, want: map[string]string{"url": MsgInvalidURL}},

		// textfield date
		{name: "date valid", extra: map[string]string{"date": `"2026-10-18"`}, want: nil},
		{name: "date wrong format", extra: map[string]string{"date": `"18/10/2026"`}, want: map[string]string{"date": MsgInvalidDate}},
		{name: "date impossible", extra: map[string]string{"date": `"2026-02-30"`}, want: map[string]string{"date": MsgInvalidDate}},

		// select
		{name: "select option", extra: map[string]string{"plan": `"pro"`}, want: nil},
		{name: "select not an option", extra: map[string]string{"plan": `"enterprise"`}, want: map[string]string{"plan": MsgNotOption}},
		{name: "select label instead of value", extra: map[string]string{"plan": `"Pro"`}, want: map[string]string{"plan": MsgNotOption}},
		{name: "select number", extra: map[string]string{"plan": `1`}, want: map[string]string{"plan": MsgNotString}},

		// select-data and autocomplete-data
		{name: "data string and number", extra: map[string]string{"country": `"br"`, "city": `3550308`}, want: nil},
		{name: "data bool", extra: map[string]string{"country": `true`}, want: map[string]string{"country": MsgNotScalar}},
		{name: "data object", extra: map[string]string{"city": `{"id":1}`}, want: map[string]string{"city": MsgNotScalar}},
		{name: "data array", extra: map[string]string{"city": `["a"]`}, want: map[string]string{"city": MsgNotScalar}},

		// toggle
		{name: "toggle bools", extra: map[string]string{"newsletter": `false`}, want: nil},
		{name: "toggle string", extra: map[string]string{"newsletter": `"true"`}, want: map[string]string{"newsletter": MsgNotBool}},
		{name: "toggle number", extra: map[string]string{"newsletter": `1`}, want: map[string]string{"newsletter": MsgNotBool}},

		// file
		{name: "file id", extra: map[string]string{"photo": `"` + fileB + `"`}, want: nil},
		{name: "file not an id", extra: map[string]string{"photo": `"../etc/passwd"`}, want: map[string]string{"photo": MsgNotFileID}},
		{name: "file array on single", extra: map[string]string{"photo": `["` + fileA + `"]`}, want: map[string]string{"photo": MsgNotFileID}},
		{name: "files ids", extra: map[string]string{"docs": `["` + fileA + `","` + fileB + `"]`}, want: nil},
		{name: "files string on multiple", extra: map[string]string{"docs": `"` + fileA + `"`}, want: map[string]string{"docs": MsgNotFileIDs}},
		{name: "files bad id", extra: map[string]string{"docs": `["` + fileA + `","x"]`}, want: map[string]string{"docs": MsgNotFileIDs}},
		{name: "files null item", extra: map[string]string{"docs": `[null]`}, want: map[string]string{"docs": MsgNotFileIDs}},
		{name: "files numbers", extra: map[string]string{"docs": `[1]`}, want: map[string]string{"docs": MsgNotFileIDs}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidatePayload(c, payloadWith(tt.extra))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ValidatePayload = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidatePayloadEmptyForm(t *testing.T) {
	c, err := ParseContent(json.RawMessage(`{"version":1,"fields":[]}`))
	if err != nil {
		t.Fatalf("ParseContent: %v", err)
	}
	if got := ValidatePayload(c, map[string]json.RawMessage{}); got != nil {
		t.Errorf("empty payload = %v, want nil", got)
	}
	got := ValidatePayload(c, map[string]json.RawMessage{"a": json.RawMessage(`1`)})
	if want := map[string]string{"a": MsgUnknownField}; !reflect.DeepEqual(got, want) {
		t.Errorf("unknown key = %v, want %v", got, want)
	}
}

func TestFileIDs(t *testing.T) {
	c, err := ParseContent(json.RawMessage(validationContent))
	if err != nil {
		t.Fatalf("ParseContent: %v", err)
	}
	p := payloadWith(map[string]string{
		"photo": `"` + fileB + `"`,
		"docs":  `["` + fileB + `","` + fileA + `"]`,
		"text":  `"` + fileA + `"`,
	})
	got := FileIDs(c, p)
	want := map[string][]string{"photo": {fileB}, "docs": {fileB, fileA}, "req_photo": {fileA}, "req_docs": {fileA}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FileIDs = %v, want %v", got, want)
	}
	if got := FileIDs(c, payloadWith(map[string]string{"req_photo": `null`, "req_docs": ""})); got != nil {
		t.Errorf("FileIDs without files = %v, want nil", got)
	}
}
