package syncer

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// Credential files live in the private credentials directory of the store,
// one JSON file per account named by its random secret reference.
const (
	credentialsDir      = "credentials"
	maxCredentialsBytes = 32768
)

// SaveAccount verifies a GitHub credential and stores it under a new account
// ID. The returned account never carries the secret reference.
func (m *Manager) SaveAccount(ctx context.Context, id string, secret github.Secret) (domain.GitHubAccount, error) {
	if !domain.ValidID(id) {
		return domain.GitHubAccount{}, domain.ErrInvalid
	}
	login, e := m.github.VerifyAccount(ctx, secret)
	if e != nil {
		return domain.GitHubAccount{}, e
	}
	ref, e := store.RandomID()
	if e != nil {
		return domain.GitHubAccount{}, e
	}
	b, e := json.Marshal(secret)
	if e != nil {
		return domain.GitHubAccount{}, e
	}
	account := domain.GitHubAccount{ID: id, Kind: secret.Kind, Login: login, SecretRef: ref}
	e = m.store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		if _, exists := db.Accounts[id]; exists {
			return domain.ErrConflict
		}
		if err := store.WritePrivateFile(filepath.Join(m.store.Dir(), credentialsDir), ref+".json", b); err != nil {
			return err
		}
		db.Accounts[id] = account
		return nil
	})
	if e != nil {
		return domain.GitHubAccount{}, e
	}
	account.SecretRef = ""
	return account, nil
}

// Accounts lists the GitHub accounts without their secret references.
func (m *Manager) Accounts() []domain.GitHubAccount {
	out := []domain.GitHubAccount{}
	m.read(func(db *domain.Database) {
		for _, a := range db.Accounts {
			a.SecretRef = ""
			out = append(out, a)
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// secret reads the credential of an account. An empty account ID means
// anonymous access. Errors never carry the secret or its file name.
func (m *Manager) secret(accountID string) (*github.Secret, domain.GitHubAccount, error) {
	if accountID == "" {
		return nil, domain.GitHubAccount{}, nil
	}
	var a domain.GitHubAccount
	var ok bool
	m.read(func(db *domain.Database) { a, ok = db.Accounts[accountID] })
	if !ok || len(a.SecretRef) != 32 || strings.ContainsAny(a.SecretRef, "/\\.") {
		return nil, a, &github.RemoteError{Code: "credentials_missing"}
	}
	filename := filepath.Join(m.store.Dir(), credentialsDir, a.SecretRef+".json")
	info, e := os.Lstat(filename)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, a, &github.RemoteError{Code: "credentials_missing"}
	}
	f, e := os.Open(filename)
	if e != nil {
		return nil, a, &github.RemoteError{Code: "credentials_missing"}
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, maxCredentialsBytes+1))
	if e != nil || len(b) > maxCredentialsBytes {
		return nil, a, domain.ErrInvalid
	}
	var secret github.Secret
	if json.Unmarshal(b, &secret) != nil || secret.Kind != a.Kind {
		return nil, a, domain.ErrInvalid
	}
	return &secret, a, nil
}

// Connections lists the GitHub connections by ID.
func (m *Manager) Connections() []domain.GitHubConnection {
	out := []domain.GitHubConnection{}
	m.read(func(db *domain.Database) {
		for _, c := range db.Connections {
			out = append(out, c)
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Jobs lists the write outbox by source, path and revision.
func (m *Manager) Jobs() []domain.SyncJob {
	out := []domain.SyncJob{}
	m.read(func(db *domain.Database) {
		for _, j := range db.Outbox {
			// Older versions stored a successful ref-update response here before
			// re-reading the branch. Only completed jobs have a confirmed pull.
			if j.State != "complete" {
				j.ObservedCommit = ""
			}
			out = append(out, j)
		}
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].SourceID != out[j].SourceID {
			return out[i].SourceID < out[j].SourceID
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Revision < out[j].Revision
	})
	return out
}
