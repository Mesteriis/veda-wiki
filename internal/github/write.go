package github

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// BlobDigest returns the Git object ID of a blob; length selects SHA-1 (40) or
// SHA-256 (64) repositories.
func BlobDigest(data []byte, length int) string {
	prefix := []byte(fmt.Sprintf("blob %d\x00", len(data)))
	if length == 40 {
		h := sha1.New()
		h.Write(prefix)
		h.Write(data)
		return hex.EncodeToString(h.Sum(nil))
	}
	h := sha256.New()
	h.Write(prefix)
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// updateBranch uses an exact old-head precondition; REST's non-force update
// alone also accepts a branch rewound to an ancestor of the prepared commit.
func (g *Client) UpdateBranch(ctx context.Context, repo, branch, before, after, token string) error {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || !domain.ValidBranch(branch) || !domain.ValidCommitID(before) || !domain.ValidCommitID(after) {
		return domain.ErrInvalid
	}
	var lookup struct {
		Data struct {
			Repository *struct {
				ID string `json:"id"`
			} `json:"repository"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	query := `query($owner:String!, $name:String!) { repository(owner:$owner, name:$name) { id } }`
	if err := g.call(ctx, "POST", "/graphql", token, map[string]any{"query": query, "variables": map[string]string{"owner": parts[0], "name": parts[1]}}, &lookup); err != nil {
		return err
	}
	if len(lookup.Errors) != 0 || lookup.Data.Repository == nil || lookup.Data.Repository.ID == "" {
		return &RemoteError{Code: "repository_unavailable"}
	}
	var result struct {
		Data struct {
			UpdateRefs *struct {
				ClientMutationID *string `json:"clientMutationId"`
			} `json:"updateRefs"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	mutation := `mutation($input:UpdateRefsInput!) { updateRefs(input:$input) { clientMutationId } }`
	input := map[string]any{"repositoryId": lookup.Data.Repository.ID, "refUpdates": []map[string]any{{"name": "refs/heads/" + branch, "beforeOid": before, "afterOid": after, "force": false}}}
	if err := g.call(ctx, "POST", "/graphql", token, map[string]any{"query": mutation, "variables": map[string]any{"input": input}}, &result); err != nil {
		return err
	}
	// GraphQL may return errors alongside HTTP 200. Leave the prepared intent
	// intact: a later complete pull must resolve an uncertain result.
	if len(result.Errors) != 0 || result.Data.UpdateRefs == nil {
		return &RemoteError{Code: "upstream_conflict"}
	}
	return nil
}

// Push commits one file at path p on top of base and moves branch to the new
// commit, returning its ID. prepare may veto the write before it is sent.
func (g *Client) Push(ctx context.Context, repo, branch, base, treeSHA, p, content, token string, prepare func(string) error) (string, error) {
	return g.PushFiles(ctx, repo, branch, base, treeSHA, map[string][]byte{p: []byte(content)}, token, prepare)
}

// PushFiles commits all files on top of base in one commit and moves branch
// to it, returning the new commit ID. prepare may veto the write before it is
// sent.
func (g *Client) PushFiles(ctx context.Context, repo, branch, base, treeSHA string, files map[string][]byte, token string, prepare func(string) error) (string, error) {
	paths := make([]string, 0, len(files))
	for name := range files {
		if !domain.SafeRelative(name) {
			return "", domain.ErrInvalid
		}
		paths = append(paths, name)
	}
	sort.Strings(paths)
	entries := make([]map[string]string, 0, len(paths))
	created := map[string]string{}
	for _, name := range paths {
		digestKey := domain.Digest(files[name])
		sha, ok := created[digestKey]
		if !ok {
			var blob struct {
				SHA string `json:"sha"`
			}
			if e := g.call(ctx, "POST", "/repos/"+repo+"/git/blobs", token, map[string]string{"content": base64.StdEncoding.EncodeToString(files[name]), "encoding": "base64"}, &blob); e != nil {
				return "", e
			}
			if !domain.ValidCommitID(blob.SHA) || BlobDigest(files[name], len(blob.SHA)) != blob.SHA {
				return "", domain.ErrInvalid
			}
			sha = blob.SHA
			created[digestKey] = sha
		}
		entries = append(entries, map[string]string{"path": name, "mode": "100644", "type": "blob", "sha": sha})
	}
	var tree struct {
		SHA string `json:"sha"`
	}
	if e := g.call(ctx, "POST", "/repos/"+repo+"/git/trees", token, map[string]any{"base_tree": treeSHA, "tree": entries}, &tree); e != nil {
		return "", e
	}
	if !domain.ValidCommitID(tree.SHA) {
		return "", domain.ErrInvalid
	}
	var commit Commit
	if e := g.call(ctx, "POST", "/repos/"+repo+"/git/commits", token, map[string]any{"message": "Update documentation through VEDA", "tree": tree.SHA, "parents": []string{base}}, &commit); e != nil {
		return "", e
	}
	if !domain.ValidCommitID(commit.SHA) {
		return "", domain.ErrInvalid
	}
	if e := prepare(commit.SHA); e != nil {
		return "", e
	}
	if e := g.UpdateBranch(ctx, repo, branch, base, commit.SHA, token); e != nil {
		return "", e
	}
	return commit.SHA, nil
}

// remove creates a deletion commit only when the complete source tree contains
// exactly the expected regular file. The caller must persist the prepared
// commit and observe the deletion in a subsequent complete pull.
func VerifiedTreeFile(source Tree, p, expected string) (string, error) {
	if source.Truncated || !domain.ValidCommitID(source.SHA) {
		return "", domain.ErrInvalid
	}
	matches := 0
	mode := ""
	for _, item := range source.Tree {
		if item.Path != p {
			continue
		}
		matches++
		if item.Type != "blob" || (item.Mode != "100644" && item.Mode != "100755") || !domain.ValidCommitID(item.SHA) || BlobDigest([]byte(expected), len(item.SHA)) != item.SHA {
			return "", domain.ErrConflict
		}
		mode = item.Mode
	}
	if matches != 1 {
		return "", domain.ErrConflict
	}
	return mode, nil
}

// Remove commits the deletion of the file p on top of base, provided its blob
// still matches expected, and returns the new commit ID.
func (g *Client) Remove(ctx context.Context, repo, branch, base string, source Tree, p, expected, token string, prepare func(string) error) (string, error) {
	if !domain.ValidCommitID(base) {
		return "", domain.ErrInvalid
	}
	mode, err := VerifiedTreeFile(source, p, expected)
	if err != nil {
		return "", err
	}
	var tree struct {
		SHA string `json:"sha"`
	}
	entry := map[string]any{"path": p, "mode": mode, "type": "blob", "sha": nil}
	if e := g.call(ctx, "POST", "/repos/"+repo+"/git/trees", token, map[string]any{"base_tree": source.SHA, "tree": []map[string]any{entry}}, &tree); e != nil {
		return "", e
	}
	if !domain.ValidCommitID(tree.SHA) {
		return "", domain.ErrInvalid
	}
	var commit Commit
	if e := g.call(ctx, "POST", "/repos/"+repo+"/git/commits", token, map[string]any{"message": "Move documentation through VEDA", "tree": tree.SHA, "parents": []string{base}}, &commit); e != nil {
		return "", e
	}
	if !domain.ValidCommitID(commit.SHA) {
		return "", domain.ErrInvalid
	}
	if e := prepare(commit.SHA); e != nil {
		return "", e
	}
	if e := g.UpdateBranch(ctx, repo, branch, base, commit.SHA, token); e != nil {
		return "", e
	}
	return commit.SHA, nil
}
