package syncer

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// ErrLocalImportActive rejects adopting the source the local importer owns.
var ErrLocalImportActive = errors.New("local importer is configured for source")

// ConnectRequest connects a source to a GitHub repository branch.
type ConnectRequest struct {
	ID            string `json:"id"`
	Repository    string `json:"repository"`
	AccountID     string `json:"account_id,omitempty"`
	Branch        string `json:"branch,omitempty"`
	Write         bool   `json:"write"`
	AdoptExisting bool   `json:"adopt_existing,omitempty"`
}

// ConfigureRequest changes the account or write access of a connection.
type ConfigureRequest struct {
	ID        string  `json:"id"`
	AccountID *string `json:"account_id"`
	Write     *bool   `json:"write"`
	Expected  *int    `json:"expected_config_revision"`
}

// Configure changes a connection at the expected configuration revision,
// verifying the repository again when the account or write access needs it.
func (m *Manager) Configure(ctx context.Context, in ConfigureRequest) (domain.GitHubConnection, error) {
	if in.Expected == nil || (in.AccountID == nil && in.Write == nil) {
		return domain.GitHubConnection{}, domain.ErrInvalid
	}
	var c domain.GitHubConnection
	var ok bool
	m.read(func(db *domain.Database) { c, ok = db.Connections[in.ID] })
	if !ok {
		return c, domain.ErrNotFound
	}
	if c.ConfigRevision != *in.Expected {
		return c, domain.ErrConflict
	}
	accountID, write := c.AccountID, c.Write
	if in.AccountID != nil {
		accountID = *in.AccountID
	}
	if in.Write != nil {
		write = *in.Write
	}
	if (write || accountID != c.AccountID) && accountID == c.AccountID && c.LastError == "rate_limited" {
		if due, err := time.Parse(time.RFC3339Nano, c.NextAttempt); err == nil && due.After(time.Now()) {
			return c, domain.ErrConflict
		}
	}
	var info *github.RepositoryInfo
	if write || accountID != c.AccountID {
		secret, account, e := m.secret(accountID)
		if e != nil {
			return c, e
		}
		if write && secret == nil {
			return c, domain.ErrInvalid
		}
		if secret != nil && secret.Kind == "app" && !strings.EqualFold(account.Login, strings.Split(c.Repository, "/")[0]) {
			return c, domain.ErrInvalid
		}
		token, e := m.github.Token(ctx, secret, c.Repository, write)
		if e != nil {
			return c, e
		}
		repository, e := m.github.Repository(ctx, c.Repository, token)
		if e != nil {
			return c, e
		}
		if repository.ID != c.RepositoryID || write && repository.Archived {
			return c, domain.ErrConflict
		}
		info = &repository
	}
	err := m.store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		if domain.MoveDeletionLocksConnection(*db, c.ID) {
			return domain.ErrConflict
		}
		current, ok := db.Connections[c.ID]
		if !ok || current.ConfigRevision != *in.Expected {
			return domain.ErrConflict
		}
		c = current
		// Reconfiguring the same account must not circumvent GitHub's cooldown.
		keepCooldown := c.AccountID == accountID && c.LastError == "rate_limited"
		c.AccountID = accountID
		c.Write = write
		c.ConfigRevision++
		if c.Paused {
			c.State = "paused"
		} else {
			c.State = "pending"
		}
		if !keepCooldown {
			c.NextAttempt = ""
			c.LastError = ""
		}
		c.LastPull = ""
		c.TreeSHA = ""
		if info != nil {
			c.Private = *info.Private
			c.Verification = "verified"
			c.VerifiedAt = time.Now().UTC().Format(time.RFC3339Nano)
			if c.Private {
				src, found := db.Sources[c.ID]
				if found {
					src.Visibility = "private"
					db.Sources[c.ID] = src
				}
			}
		}
		db.Connections[c.ID] = c
		for id, j := range db.Outbox {
			if j.SourceID == c.ID {
				if !keepCooldown {
					j.NextAttempt = ""
				}
				if j.State == "blocked" {
					j.State = "pending"
					j.LastError = ""
				}
				db.Outbox[id] = j
			}
		}
		return nil
	})
	if err != nil {
		return c, err
	}
	m.Wake()
	return c, nil
}

// Connect verifies a repository and adds its connection. With AdoptExisting
// it attaches an existing private local source whose content matches the
// complete private repository, in one import transaction.
func (m *Manager) Connect(ctx context.Context, in ConnectRequest) (domain.GitHubConnection, error) {
	if !domain.IDPattern.MatchString(in.ID) {
		return domain.GitHubConnection{}, domain.ErrInvalid
	}
	var snapshot string
	if in.AdoptExisting {
		if in.ID == m.LocalImportSource {
			return domain.GitHubConnection{}, ErrLocalImportActive
		}
		if in.AccountID == "" {
			return domain.GitHubConnection{}, domain.ErrInvalid
		}
		var err error
		snapshot, err = m.adoptionSnapshot(in.ID)
		if err != nil {
			return domain.GitHubConnection{}, err
		}
	}
	repo, e := github.RepositoryName(in.Repository)
	if e != nil {
		return domain.GitHubConnection{}, e
	}
	secret, account, e := m.secret(in.AccountID)
	if e != nil {
		return domain.GitHubConnection{}, e
	}
	if secret != nil && secret.Kind == "app" && !strings.EqualFold(account.Login, strings.Split(repo, "/")[0]) {
		return domain.GitHubConnection{}, domain.ErrInvalid
	}
	if in.Write && secret == nil {
		return domain.GitHubConnection{}, domain.ErrInvalid
	}
	token, e := m.github.Token(ctx, secret, repo, in.Write)
	if e != nil {
		return domain.GitHubConnection{}, e
	}
	info, e := m.github.Repository(ctx, repo, token)
	if e != nil {
		return domain.GitHubConnection{}, e
	}
	branch := in.Branch
	if branch == "" {
		branch = info.DefaultBranch
	}
	if !github.ValidBranch(branch) || info.Archived && in.Write {
		return domain.GitHubConnection{}, domain.ErrInvalid
	}
	head, e := m.github.Head(ctx, repo, branch, token)
	if e != nil {
		return domain.GitHubConnection{}, e
	}
	c := domain.GitHubConnection{ID: in.ID, Repository: info.FullName, RepositoryID: info.ID, Branch: branch, AccountID: in.AccountID, Write: in.Write, Private: *info.Private, Verification: "verified", VerifiedAt: time.Now().UTC().Format(time.RFC3339Nano), State: "pending", ConfigRevision: 1}
	if in.AdoptExisting {
		return m.adopt(ctx, c, account, snapshot, head, token)
	}
	err := m.store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		if _, ok := db.Connections[c.ID]; ok {
			return domain.ErrConflict
		}
		if _, ok := db.Sources[c.ID]; ok {
			return domain.ErrConflict
		}
		db.Connections[c.ID] = c
		return nil
	})
	if err != nil {
		return c, err
	}
	m.Wake()
	return c, nil
}

// adopt imports the complete private repository over the existing source
// and attaches the connection in the same transaction.
func (m *Manager) adopt(ctx context.Context, c domain.GitHubConnection, account domain.GitHubAccount, snapshot, head, token string) (domain.GitHubConnection, error) {
	if !c.Private {
		return domain.GitHubConnection{}, domain.ErrConflict
	}
	tree, err := m.github.Tree(ctx, c.Repository, head, token)
	if err != nil {
		return domain.GitHubConnection{}, err
	}
	batch, err := m.remoteBatch(ctx, c, head, token, tree)
	if err != nil {
		return domain.GitHubConnection{}, err
	}
	if batch.Create || !batch.Complete || batch.Withheld != 0 {
		return domain.GitHubConnection{}, domain.ErrConflict
	}
	// Verify the remote identity, visibility and head again immediately
	// before the atomic attachment; a later sync still re-verifies them.
	latest, err := m.github.Repository(ctx, c.Repository, token)
	if err != nil {
		return domain.GitHubConnection{}, err
	}
	if latest.ID != c.RepositoryID || !*latest.Private || latest.Archived && c.Write {
		return domain.GitHubConnection{}, domain.ErrConflict
	}
	currentHead, err := m.github.Head(ctx, c.Repository, c.Branch, token)
	if err != nil {
		return domain.GitHubConnection{}, err
	}
	if currentHead != head {
		return domain.GitHubConnection{}, domain.ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return domain.GitHubConnection{}, err
	}
	c.VerifiedAt = time.Now().UTC().Format(time.RFC3339Nano)
	proof := remoteProof(c, "private", batch.Commit, tree.SHA)
	proof.Adoption = adoptionProof(ctx, c, account, snapshot)
	if _, err = m.importer.Apply(batch, proof, false); err != nil {
		return domain.GitHubConnection{}, err
	}
	m.Wake()
	for _, saved := range m.Connections() {
		if saved.ID == c.ID {
			return saved, nil
		}
	}
	return domain.GitHubConnection{}, domain.ErrConflict
}
