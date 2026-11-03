package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// errInvalid marks a request the adapter refuses with the generic message.
var errInvalid = errors.New("invalid input, unavailable source, authorization or revision conflict")

// member is one key of an object whose key order matters.
type member struct {
	key   string
	value any
}

// object is a JSON object that keeps its key order, as a Python dict does.
type object []member

// MarshalJSON writes the members in order without HTML escaping.
func (o object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := marshal(m.key)
		if err != nil {
			return nil, err
		}
		value, err := marshal(m.value)
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(value)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// get returns the value of key, like Python's dict[key].
func (o object) get(key string) (any, error) {
	for _, m := range o {
		if m.key == key {
			return m.value, nil
		}
	}
	return nil, fmt.Errorf("%w: missing %s", errInvalid, key)
}

// set replaces the value of an existing key in place or appends the key.
func (o *object) set(key string, value any) {
	for i := range *o {
		if (*o)[i].key == key {
			(*o)[i].value = value
			return
		}
	}
	*o = append(*o, member{key, value})
}

// decodeObject decodes a JSON object keeping key order; values stay raw. A
// repeated key keeps its first position and its last value.
func decodeObject(raw json.RawMessage) (object, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("%w: not an object", errInvalid)
	}
	var out object
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, _ := token.(string)
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		out.set(key, value)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return out, nil
}

// decodeArray decodes a JSON array; elements stay raw.
func decodeArray(raw any) ([]json.RawMessage, error) {
	data, ok := raw.(json.RawMessage)
	if !ok {
		return nil, errInvalid
	}
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil || items == nil {
		return nil, fmt.Errorf("%w: not an array", errInvalid)
	}
	return items, nil
}

// pyValue decodes a raw JSON value with numbers kept as json.Number.
func pyValue(raw any) any {
	data, ok := raw.(json.RawMessage)
	if !ok {
		return raw
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil
	}
	return value
}

// truthy applies Python truthiness to a JSON value.
func truthy(raw any) bool {
	switch v := pyValue(raw).(type) {
	case nil:
		return false
	case bool:
		return v
	case json.Number:
		f, err := v.Float64()
		return err != nil || f != 0
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	}
	return true
}

// equalsInt applies Python == between a JSON value and an integer: numbers
// compare by value and booleans as 0 and 1.
func equalsInt(raw any, n int64) bool {
	switch v := pyValue(raw).(type) {
	case bool:
		return (v && n == 1) || (!v && n == 0)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i == n
		}
		f, err := v.Float64()
		return err == nil && f == float64(n)
	}
	return false
}

// marshal encodes v as compact JSON without HTML escaping.
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// pythonJSON reformats JSON as Python's json.dumps(value, ensure_ascii=False)
// prints it: ", " and ": " separators, non-ASCII kept, and only quotes,
// backslashes and control characters escaped. Number literals are kept.
func pythonJSON(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var out bytes.Buffer
	if err := writePython(decoder, &out); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing JSON data")
	}
	return out.Bytes(), nil
}

func writePython(decoder *json.Decoder, out *bytes.Buffer) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch v := token.(type) {
	case json.Delim:
		out.WriteRune(rune(v))
		for first := true; decoder.More(); first = false {
			if !first {
				out.WriteString(", ")
			}
			if v == '{' {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, _ := key.(string)
				writePythonString(out, name)
				out.WriteString(": ")
			}
			if err := writePython(decoder, out); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		out.WriteRune(rune(end.(json.Delim)))
	case string:
		writePythonString(out, v)
	case json.Number:
		out.WriteString(v.String())
	case bool:
		out.WriteString(strconv.FormatBool(v))
	case nil:
		out.WriteString("null")
	}
	return nil
}

func writePythonString(out *bytes.Buffer, s string) {
	out.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		default:
			if r < 0x20 {
				fmt.Fprintf(out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
}
