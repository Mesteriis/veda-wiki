package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The Go SDK does not validate tool input for a raw ToolHandler, so every
// call is checked here against the tool's own schema before any request.
// Only the keywords the tool table uses are supported; an unsupported
// keyword makes compileSchema fail.

type propertySchema struct {
	Type        string          `json:"type"`
	Description string          `json:"description"`
	MinLength   *int            `json:"minLength"`
	MaxLength   *int            `json:"maxLength"`
	Minimum     *int64          `json:"minimum"`
	Maximum     *int64          `json:"maximum"`
	Const       json.RawMessage `json:"const"`
	Enum        []string        `json:"enum"`
	Pattern     string          `json:"pattern"`
	pattern     *regexp.Regexp
}

type inputSchema struct {
	Type                 string                     `json:"type"`
	Properties           map[string]*propertySchema `json:"properties"`
	Required             []string                   `json:"required"`
	AdditionalProperties bool                       `json:"additionalProperties"`
}

// compileSchema parses a tool schema, rejecting keywords the validator does
// not implement.
func compileSchema(raw json.RawMessage) (*inputSchema, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var schema inputSchema
	if err := decoder.Decode(&schema); err != nil {
		return nil, err
	}
	if schema.Type != "object" || schema.AdditionalProperties {
		return nil, errors.New("schema must be a closed object")
	}
	for name, property := range schema.Properties {
		switch property.Type {
		case "string", "integer", "boolean":
		case "":
			if property.Enum == nil {
				return nil, fmt.Errorf("property %s has neither type nor enum", name)
			}
		default:
			return nil, fmt.Errorf("property %s has unsupported type %q", name, property.Type)
		}
		if property.Const != nil && string(property.Const) != "true" {
			return nil, fmt.Errorf("property %s has unsupported const", name)
		}
		if property.Pattern != "" {
			pattern, err := regexp.Compile(property.Pattern)
			if err != nil {
				return nil, err
			}
			property.pattern = pattern
		}
	}
	for _, name := range schema.Required {
		if schema.Properties[name] == nil {
			return nil, fmt.Errorf("required property %s is not declared", name)
		}
	}
	return &schema, nil
}

// arguments holds validated tool input: strings, booleans and int64 values.
type arguments map[string]any

func (a arguments) str(name string) string {
	value, _ := a[name].(string)
	return value
}

func (a arguments) integer(name string) int64 {
	value, _ := a[name].(int64)
	return value
}

// query encodes the arguments as a URL query, as Python's urlencode would
// render these value types.
func (a arguments) query() string {
	values := url.Values{}
	for name, value := range a {
		switch v := value.(type) {
		case string:
			values.Set(name, v)
		case int64:
			values.Set(name, strconv.FormatInt(v, 10))
		case bool:
			values.Set(name, map[bool]string{true: "True", false: "False"}[v])
		}
	}
	return values.Encode()
}

// parse validates raw tool arguments. Absent or null arguments are an empty
// object, as in the Python SDK.
func (s *inputSchema) parse(raw json.RawMessage) (arguments, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := decoder.Decode(&decoded); err != nil {
			return nil, invalidArguments("arguments must be a JSON object")
		}
		if _, err := decoder.Token(); err != io.EOF {
			return nil, invalidArguments("arguments must be a JSON object")
		}
	}
	fields, ok := decoded.(map[string]any)
	if decoded == nil {
		fields, ok = map[string]any{}, true
	}
	if !ok {
		return nil, invalidArguments("arguments must be a JSON object")
	}
	args := arguments{}
	for _, name := range slices.Sorted(maps.Keys(fields)) { // stable reasons
		value := fields[name]
		property := s.Properties[name]
		if property == nil {
			return nil, invalidArguments("unknown property " + quoteName(name))
		}
		checked, err := property.check(name, value)
		if err != nil {
			return nil, err
		}
		args[name] = checked
	}
	for _, name := range s.Required {
		if _, ok := args[name]; !ok {
			return nil, invalidArguments("missing required property " + quoteName(name))
		}
	}
	return args, nil
}

// check validates one value and returns it as string, bool or int64.
func (p *propertySchema) check(name string, value any) (any, error) {
	property := quoteName(name)
	var checked any
	switch p.Type {
	case "string":
		s, ok := value.(string)
		if !ok {
			return nil, invalidArguments("property " + property + " has the wrong type")
		}
		length := utf8.RuneCountInString(s) // JSON Schema counts code points.
		switch {
		case p.MinLength != nil && length < *p.MinLength:
			return nil, invalidArguments("property " + property + " is too short")
		case p.MaxLength != nil && length > *p.MaxLength:
			return nil, invalidArguments("property " + property + " is too long")
		case p.pattern != nil && !p.pattern.MatchString(s):
			return nil, invalidArguments("property " + property + " does not match the required pattern")
		}
		checked = s
	case "integer":
		n, ok := integer(value)
		if !ok {
			return nil, invalidArguments("property " + property + " has the wrong type")
		}
		if (p.Minimum != nil && n < *p.Minimum) || (p.Maximum != nil && n > *p.Maximum) {
			return nil, invalidArguments("property " + property + " out of range")
		}
		checked = n
	case "boolean":
		b, ok := value.(bool)
		if !ok {
			return nil, invalidArguments("property " + property + " has the wrong type")
		}
		checked = b
	default:
		checked = value
	}
	if p.Const != nil && checked != true {
		return nil, invalidArguments("property " + property + " must be true")
	}
	if p.Enum != nil {
		s, ok := checked.(string)
		if !ok || !slices.Contains(p.Enum, s) {
			return nil, invalidArguments("property " + property + " is not an allowed value")
		}
	}
	return checked, nil
}

// integer accepts a JSON number with an integral value within int64, as JSON
// Schema's "integer" accepts 2.0.
func integer(value any) (int64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	if n, err := number.Int64(); err == nil {
		return n, true
	}
	f, err := number.Float64()
	if err != nil || f != math.Trunc(f) || f < math.MinInt64 || f >= math.MaxInt64 {
		return 0, false
	}
	return int64(f), true
}

// validationError is an argument failure whose reason names a property but
// never carries an argument value.
type validationError struct{ reason string }

func (e *validationError) Error() string { return e.reason }

func invalidArguments(reason string) error { return &validationError{reason} }

// quoteName renders a property name for a reason. Names the caller chose are
// kept only when short and printable.
func quoteName(name string) string {
	if utf8.RuneCountInString(name) > 64 || strings.ContainsFunc(name, func(r rune) bool { return !unicode.IsPrint(r) || r == '\'' }) {
		return "'?'"
	}
	return "'" + name + "'"
}
