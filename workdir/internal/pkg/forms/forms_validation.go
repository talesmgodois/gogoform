package forms

import (
	"bytes"
	"encoding/json"
	"math"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Field-level messages returned by ValidatePayload.
const (
	MsgUnknownField  = "unknown field"
	MsgRequired      = "is required"
	MsgNotString     = "must be a string"
	MsgNotBool       = "must be true or false"
	MsgNotScalar     = "must be a string or a number"
	MsgNotOption     = "must be one of the allowed options"
	MsgInvalidEmail  = "must be a valid e-mail address"
	MsgInvalidNumber = "must be a number"
	MsgInvalidTel    = "must be a valid phone number"
	MsgInvalidURL    = "must be a valid http or https URL"
	MsgInvalidDate   = "must be a date formatted as YYYY-MM-DD"
	MsgNotFileID     = "must be a file ID"
	MsgNotFileIDs    = "must be a list of file IDs"
)

// telRe matches a phone number: an optional leading +, then digits and the
// usual separators; at least 3 digits are checked separately.
var telRe = regexp.MustCompile(`^\+?[0-9 ().\-]+$`)

// fileIDRe matches the random UUIDs file IDs are made of.
var fileIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidatePayload checks the answers of a submission against the form
// definition and returns a message per offending key, or nil when the
// payload is valid. Keys that are not input fields of c (layout components
// included) are rejected; a null value counts as no answer; required fields
// need a non-empty answer, and a required toggle must be true. File fields
// take file IDs only: checking that the files exist is left to the caller
// (see FileIDs).
func ValidatePayload(c Content, payload map[string]json.RawMessage) map[string]string {
	errs := map[string]string{}
	for key := range payload {
		if _, ok := c.Field(key); !ok {
			errs[key] = MsgUnknownField
		}
	}
	walkInputs(c.Fields, func(f Field) {
		raw, ok := payload[f.Name]
		if ok && isNull(raw) {
			ok = false
		}
		var msg string
		if ok {
			msg = validateValue(f, raw)
		} else if f.Required {
			msg = MsgRequired
		}
		if msg != "" {
			errs[f.Name] = msg
		}
	})
	if len(errs) == 0 {
		return nil
	}
	return errs
}

// FileIDs returns the file IDs answered to each file field of c, keyed by
// field name, or nil when there are none. It expects a payload that passed
// ValidatePayload and ignores values of any other shape.
func FileIDs(c Content, payload map[string]json.RawMessage) map[string][]string {
	var ids map[string][]string
	add := func(name string, id ...string) {
		if ids == nil {
			ids = map[string][]string{}
		}
		ids[name] = append(ids[name], id...)
	}
	walkInputs(c.Fields, func(f Field) {
		raw, ok := payload[f.Name]
		if f.Type != FieldFile || !ok {
			return
		}
		var one string
		if json.Unmarshal(raw, &one) == nil {
			if one != "" {
				add(f.Name, one)
			}
			return
		}
		var many []string
		if json.Unmarshal(raw, &many) == nil && len(many) > 0 {
			add(f.Name, many...)
		}
	})
	return ids
}

// walkInputs calls fn for every input field of fields, panels included.
func walkInputs(fields []Field, fn func(Field)) {
	for _, f := range fields {
		switch {
		case f.Type == FieldPanel:
			walkInputs(f.Fields, fn)
		case !f.Type.IsLayout():
			fn(f)
		}
	}
}

func isNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, []byte("null"))
}

// validateValue checks the non-null answer raw of field f and returns the
// error message, or "" when it is valid.
func validateValue(f Field, raw json.RawMessage) string {
	switch f.Type {
	case FieldToggle:
		var b bool
		if json.Unmarshal(raw, &b) != nil {
			return MsgNotBool
		}
		if f.Required && !b {
			return MsgRequired
		}
		return ""
	case FieldFile:
		return validateFile(f, raw)
	case FieldSelectData, FieldAutocompleteData:
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return MsgNotScalar
		}
		switch v := v.(type) {
		case string:
			if f.Required && strings.TrimSpace(v) == "" {
				return MsgRequired
			}
			return ""
		case float64:
			return ""
		}
		return MsgNotScalar
	case FieldTextfield:
		if f.InputType == InputNumber {
			return validateNumber(f, raw)
		}
	}

	var s string
	if json.Unmarshal(raw, &s) != nil {
		return MsgNotString
	}
	if strings.TrimSpace(s) == "" {
		if f.Required {
			return MsgRequired
		}
		return ""
	}
	switch f.Type {
	case FieldSelect:
		for _, o := range f.Options {
			if o.Value == s {
				return ""
			}
		}
		return MsgNotOption
	case FieldTextfield:
		return validateText(f.InputType, s)
	}
	return ""
}

// validateText checks a non-empty textfield value against its input_type.
func validateText(inputType, s string) string {
	switch inputType {
	case InputEmail:
		addr, err := mail.ParseAddress(s)
		if err != nil || addr.Address != s || addr.Name != "" {
			return MsgInvalidEmail
		}
	case InputTel:
		digits := 0
		for _, r := range s {
			if r >= '0' && r <= '9' {
				digits++
			}
		}
		if !telRe.MatchString(s) || digits < 3 {
			return MsgInvalidTel
		}
	case InputURL:
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return MsgInvalidURL
		}
	case InputDate:
		if _, err := time.Parse(time.DateOnly, s); err != nil {
			return MsgInvalidDate
		}
	}
	return ""
}

// validateNumber checks a number textfield, answered as a JSON number or as
// a numeric string.
func validateNumber(f Field, raw json.RawMessage) string {
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return MsgInvalidNumber
	}
	s = strings.TrimSpace(s)
	if s == "" {
		if f.Required {
			return MsgRequired
		}
		return ""
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
		return MsgInvalidNumber
	}
	return ""
}

// validateFile checks a file answer: one file ID, or a list of them when the
// field takes multiple files.
func validateFile(f Field, raw json.RawMessage) string {
	if !f.Multiple {
		var id string
		if json.Unmarshal(raw, &id) != nil {
			return MsgNotFileID
		}
		if id == "" {
			if f.Required {
				return MsgRequired
			}
			return ""
		}
		if !fileIDRe.MatchString(id) {
			return MsgNotFileID
		}
		return ""
	}
	var ids []string
	if json.Unmarshal(raw, &ids) != nil {
		return MsgNotFileIDs
	}
	if len(ids) == 0 {
		if f.Required {
			return MsgRequired
		}
		return ""
	}
	for _, id := range ids {
		if !fileIDRe.MatchString(id) {
			return MsgNotFileIDs
		}
	}
	return ""
}
