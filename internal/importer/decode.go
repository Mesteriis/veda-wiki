package importer

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// Decode reads a Batch from an import request body. It decodes large file
// and image maps one value at a time; json.Decoder normally buffers a complete
// top-level object, including base64-encoded images. It rejects duplicate keys
// and enforces aggregate bounds while decoding.
func Decode(rw http.ResponseWriter, r *http.Request, batch *Batch) error {
	dec := json.NewDecoder(http.MaxBytesReader(rw, r.Body, MaxRequestBytes))
	dec.DisallowUnknownFields()
	start, err := dec.Token()
	if err != nil || start != json.Delim('{') {
		return domain.ErrInvalid
	}
	seen := map[string]bool{}
	count, size := 0, 0
	decodeMap := func(images bool) error {
		start, err := dec.Token()
		if err != nil {
			return domain.ErrInvalid
		}
		if start == nil { // JSON null retains the same meaning as a nil map.
			return nil
		}
		if start != json.Delim('{') {
			return domain.ErrInvalid
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
				return domain.ErrInvalid
			}
			keys[name] = true
			count++
			if count > 2000 {
				return domain.ErrInvalid
			}
			if images {
				var value []byte
				if dec.Decode(&value) != nil || len(value) > 4<<20 {
					return domain.ErrInvalid
				}
				size += len(value)
				batch.Assets[name] = value
			} else {
				var value string
				if dec.Decode(&value) != nil || len(value) > domain.MaxArticleBytes {
					return domain.ErrInvalid
				}
				size += len(value)
				batch.Files[name] = value
			}
			if size > MaxBytes {
				return domain.ErrInvalid
			}
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim('}') {
			return domain.ErrInvalid
		}
		return nil
	}
	for dec.More() {
		token, err := dec.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] {
			return domain.ErrInvalid
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
		case "withheld_paths":
			err = dec.Decode(&batch.WithheldPaths)
		case "expected_import":
			err = dec.Decode(&batch.ExpectedImport)
		case "create":
			err = dec.Decode(&batch.Create)
		case "visibility":
			err = dec.Decode(&batch.Visibility)
		default:
			return domain.ErrInvalid
		}
		if err != nil {
			return domain.ErrInvalid
		}
	}
	end, err := dec.Token()
	if err != nil || end != json.Delim('}') {
		return domain.ErrInvalid
	}
	var extra any
	if err = dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return domain.ErrInvalid
	}
	return nil
}
