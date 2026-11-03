package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// Decode large file and image maps one value at a time. json.Decoder normally
// buffers a complete top-level object, including base64-encoded images.
// Reject duplicate keys and enforce aggregate bounds while decoding.
func decodeImport(rw http.ResponseWriter, r *http.Request, batch *ImportBatch) error {
	dec := json.NewDecoder(http.MaxBytesReader(rw, r.Body, maxImportRequestBytes))
	dec.DisallowUnknownFields()
	start, err := dec.Token()
	if err != nil || start != json.Delim('{') {
		return ErrInvalid
	}
	seen := map[string]bool{}
	count, size := 0, 0
	decodeMap := func(images bool) error {
		start, err := dec.Token()
		if err != nil {
			return ErrInvalid
		}
		if start == nil { // JSON null retains the same meaning as a nil map.
			return nil
		}
		if start != json.Delim('{') {
			return ErrInvalid
		}
		keys := map[string]bool{}
		if images {
			batch.Assets = map[string][]byte{}
		} else {
			batch.Files = map[string]string{}
		}
		for dec.More() {
			token, err := dec.Token()
			name, ok := token.(string)
			if err != nil || !ok || keys[name] {
				return ErrInvalid
			}
			keys[name] = true
			count++
			if count > 2000 {
				return ErrInvalid
			}
			if images {
				var value []byte
				if dec.Decode(&value) != nil || len(value) > 4<<20 {
					return ErrInvalid
				}
				size += len(value)
				batch.Assets[name] = value
			} else {
				var value string
				if dec.Decode(&value) != nil || len(value) > maxArticleBytes {
					return ErrInvalid
				}
				size += len(value)
				batch.Files[name] = value
			}
			if size > maxImportBytes {
				return ErrInvalid
			}
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim('}') {
			return ErrInvalid
		}
		return nil
	}
	for dec.More() {
		token, err := dec.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] {
			return ErrInvalid
		}
		seen[name] = true
		switch name {
		case "source_id":
			err = dec.Decode(&batch.SourceID)
		case "kind":
			err = dec.Decode(&batch.Kind)
		case "metadata":
			err = dec.Decode(&batch.Metadata)
		case "files":
			err = decodeMap(false)
		case "assets":
			err = decodeMap(true)
		case "commit":
			err = dec.Decode(&batch.Commit)
		case "dirty":
			err = dec.Decode(&batch.Dirty)
		case "complete":
			err = dec.Decode(&batch.Complete)
		case "withheld":
			err = dec.Decode(&batch.Withheld)
		case "expected_import":
			err = dec.Decode(&batch.ExpectedImport)
		case "create":
			err = dec.Decode(&batch.Create)
		case "visibility":
			err = dec.Decode(&batch.Visibility)
		default:
			return ErrInvalid
		}
		if err != nil {
			return ErrInvalid
		}
	}
	end, err := dec.Token()
	if err != nil || end != json.Delim('}') {
		return ErrInvalid
	}
	var extra any
	if err = dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}
