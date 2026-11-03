// Package commands implements the veda subcommands.
package commands

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// AccessKey writes new key material only to an exclusively created
// private file. The one-time server response is never sent to stdout.
func AccessKey(args []string, output io.Writer) error {
	if len(args) == 0 {
		return domain.ErrInvalid
	}
	action := args[0]
	flags := flag.NewFlagSet("access-key "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	id := flags.String("id", "", "Key identifier")
	sources := flags.String("sources", "", "Comma-separated source IDs")
	expires := flags.String("expires-at", "", "RFC3339 expiry")
	out := flags.String("out", "", "New private key file")
	base := flags.String("url", "http://127.0.0.1:8080", "Loopback VEDA endpoint")
	owner := flags.String("key-file", ".veda/owner.key", "Owner key file")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return domain.ErrInvalid
	}
	if action != "create" && action != "list" && action != "revoke" {
		return domain.ErrInvalid
	}
	if action != "list" && !domain.ValidID(*id) {
		return domain.ErrInvalid
	}
	var expiry time.Time
	var sourceIDs []string
	if action == "create" {
		var err error
		expiry, err = time.Parse(time.RFC3339, *expires)
		if err != nil || !expiry.After(time.Now()) || expiry.After(time.Now().Add(366*24*time.Hour)) || *out == "" {
			return domain.ErrInvalid
		}
		sourceIDs = strings.Split(*sources, ",")
		if len(sourceIDs) > 100 {
			return domain.ErrInvalid
		}
		seen := map[string]bool{}
		for _, id := range sourceIDs {
			if !domain.ValidID(id) || seen[id] {
				return domain.ErrInvalid
			}
			seen[id] = true
		}
	} else if *out != "" || *sources != "" || *expires != "" {
		return domain.ErrInvalid
	}
	client, err := newLocalClient(*base, *owner)
	if err != nil {
		return err
	}
	switch action {
	case "list":
		var result []auth.AccessKeyView
		if err = client.request("GET", "/api/v1/access-keys", nil, &result); err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(result)
	case "revoke":
		var result map[string]string
		if err = client.request("POST", "/api/v1/access-keys/revoke", map[string]string{"id": *id}, &result); err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(result)
	case "create":
		file, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return errors.New("cannot create new private key file")
		}
		defer file.Close()
		var result struct {
			ID      string   `json:"id"`
			Sources []string `json:"sources"`
			Expires string   `json:"expires_at"`
			Key     string   `json:"key"`
		}
		body := map[string]any{"id": *id, "sources": sourceIDs, "expires_at": expiry.UTC().Format(time.RFC3339Nano)}
		if err = client.request("POST", "/api/v1/access-keys", body, &result); err != nil {
			return errors.New("key creation failed or result is uncertain; inspect key metadata and reserved file before retrying")
		}
		if result.ID != *id {
			return errors.New("unexpected key response; inspect key metadata and reserved file")
		}
		if _, err = domain.HexKey(result.Key); err != nil {
			return errors.New("invalid key response; revoke the created key by ID")
		}
		if _, err = io.WriteString(file, result.Key); err == nil {
			err = file.Sync()
		}
		if err != nil {
			return errors.New("key created but file write failed; revoke it by ID and protect the reserved file")
		}
		if err = file.Close(); err != nil {
			return errors.New("key created but file close failed; revoke it by ID and protect the reserved file")
		}
		dir, err := os.Open(filepath.Dir(*out))
		if err != nil {
			return errors.New("key created but directory sync failed; verify file durability or revoke the key")
		}
		err = dir.Sync()
		closeErr := dir.Close()
		if err != nil || closeErr != nil {
			return errors.New("key created but directory sync failed; verify file durability or revoke the key")
		}
		return json.NewEncoder(output).Encode(auth.AccessKeyView{ID: result.ID, Sources: result.Sources, Expires: result.Expires})
	}
	return domain.ErrInvalid
}
