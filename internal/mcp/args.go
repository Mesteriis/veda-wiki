package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
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
			return nil, errInvalid
		}
		if _, err := decoder.Token(); err != io.EOF {
			return nil, errInvalid
		}
	}
	fields, ok := decoded.(map[string]any)
	if decoded == nil {
		fields, ok = map[string]any{}, true
	}
	if !ok {
		return nil, errInvalid
	}
	args := arguments{}
	for name, value := range fields {
		property := s.Properties[name]
		if property == nil {
			return nil, errInvalid
		}
		checked, err := property.check(value)
		if err != nil {
			return nil, err
		}
		args[name] = checked
	}
	for _, name := range s.Required {
		if _, ok := args[name]; !ok {
			return nil, errInvalid
		}
	}
	return args, nil
}

// check validates one value and returns it as string, bool or int64.
func (p *propertySchema) check(value any) (any, error) {
	var checked any
	switch p.Type {
	case "string":
		s, ok := value.(string)
		if !ok {
			return nil, errInvalid
		}
		length := utf8.RuneCountInString(s) // JSON Schema counts code points.
		if (p.MinLength != nil && length < *p.MinLength) || (p.MaxLength != nil && length > *p.MaxLength) ||
			(p.pattern != nil && !p.pattern.MatchString(s)) {
			return nil, errInvalid
		}
		checked = s
	case "integer":
		n, ok := integer(value)
		if !ok || (p.Minimum != nil && n < *p.Minimum) || (p.Maximum != nil && n > *p.Maximum) {
			return nil, errInvalid
		}
		checked = n
	case "boolean":
		b, ok := value.(bool)
		if !ok {
			return nil, errInvalid
		}
		checked = b
	default:
		checked = value
	}
	if p.Const != nil && checked != true {
		return nil, errInvalid
	}
	if p.Enum != nil {
		s, ok := checked.(string)
		if !ok || !slices.Contains(p.Enum, s) {
			return nil, errInvalid
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
