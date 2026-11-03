package main

import (
	"encoding/json"
	"flag"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"io"
	"os"
	"time"
)

func githubCommand(args []string) error {
	if len(args) == 0 {
		return ErrInvalid
	}
	action := args[0]
	flags := flag.NewFlagSet("github "+action, flag.ContinueOnError)
	id := flags.String("id", "", "Explicit account or connection ID")
	base := flags.String("url", "http://127.0.0.1:8080", "Loopback VEDA endpoint")
	key := flags.String("key-file", ".veda/owner.key", "Owner key file")
	secretFile := flags.String("secret-file", "", "Private GitHub credential JSON (account action only)")
	repo := flags.String("repository", "", "GitHub owner/repository or HTTPS link")
	account := flags.String("account", "", "Explicit stored account ID, empty for public anonymous import")
	branch := flags.String("branch", "", "Branch (default: repository default)")
	write := flags.Bool("write", false, "Enable background writes for this connection")
	adopt := flags.Bool("adopt-existing", false, "Attach an existing private source to a private repository")
	resume := flags.Bool("resume", false, "Resume rather than pause")
	expected := flags.Int("expected-config", 0, "Expected connection configuration revision for configure")
	if e := flags.Parse(args[1:]); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return ErrInvalid
	}
	client, e := newLocalClient(*base, *key)
	if e != nil {
		return e
	}
	var result any
	switch action {
	case "account":
		if !identifier.MatchString(*id) || *secretFile == "" {
			return ErrInvalid
		}
		info, e := os.Lstat(*secretFile)
		if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return ErrInvalid
		}
		file, e := os.Open(*secretFile)
		if e != nil {
			return e
		}
		defer file.Close()
		decoder := json.NewDecoder(io.LimitReader(file, 32769))
		decoder.DisallowUnknownFields()
		var secret github.Secret
		if e = decoder.Decode(&secret); e != nil {
			return ErrInvalid
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return ErrInvalid
		}
		body := struct {
			ID string `json:"id"`
			github.Secret
		}{*id, secret}
		if e = client.request("POST", "/api/v1/github/accounts", body, &result); e != nil {
			return e
		}
	case "connect":
		if !identifier.MatchString(*id) || *repo == "" {
			return ErrInvalid
		}
		if *adopt {
			client.http.Timeout = 6 * time.Minute
		}
		if e = client.request("POST", "/api/v1/github/connections", connectRequest{ID: *id, Repository: *repo, AccountID: *account, Branch: *branch, Write: *write, AdoptExisting: *adopt}, &result); e != nil {
			return e
		}
	case "configure":
		if !identifier.MatchString(*id) || *expected < 1 {
			return ErrInvalid
		}
		in := configureRequest{ID: *id, Expected: expected}
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "account" {
				in.AccountID = account
			}
			if f.Name == "write" {
				in.Write = write
			}
		})
		if e = client.request("PUT", "/api/v1/github/connections", in, &result); e != nil {
			return e
		}
	case "status":
		var conns, accounts, outbox any
		if e = client.request("GET", "/api/v1/github/accounts", nil, &accounts); e != nil {
			return e
		}
		if e = client.request("GET", "/api/v1/github/connections", nil, &conns); e != nil {
			return e
		}
		if e = client.request("GET", "/api/v1/github/outbox", nil, &outbox); e != nil {
			return e
		}
		result = map[string]any{"accounts": accounts, "connections": conns, "outbox": outbox}
	case "sync":
		if !identifier.MatchString(*id) {
			return ErrInvalid
		}
		if e = client.request("POST", "/api/v1/github/sync", map[string]string{"id": *id}, &result); e != nil {
			return e
		}
	case "pause":
		if !identifier.MatchString(*id) {
			return ErrInvalid
		}
		if e = client.request("POST", "/api/v1/github/pause", map[string]any{"id": *id, "paused": !*resume}, &result); e != nil {
			return e
		}
	default:
		return ErrInvalid
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
