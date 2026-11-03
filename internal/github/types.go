package github

// Secret is credential material for a GitHub account. It is never returned in
// API responses; the store keeps only an opaque reference to it.
type Secret struct {
	Kind           string `json:"kind"`
	Token          string `json:"token,omitempty"`
	AppID          int64  `json:"app_id,omitempty"`
	InstallationID int64  `json:"installation_id,omitempty"`
	PrivateKey     string `json:"private_key,omitempty"`
}

// RepositoryInfo is the validated subset of a repository response.
type RepositoryInfo struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	Private       *bool  `json:"private"`
	DefaultBranch string `json:"default_branch"`
	Archived      bool   `json:"archived"`
	Permissions   struct {
		Push bool `json:"push"`
	} `json:"permissions"`
	Visibility string `json:"visibility"`
}

// TreeEntry is one path of a Git tree.
type TreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

// Tree is a complete recursive Git tree.
type Tree struct {
	SHA       string      `json:"sha"`
	Tree      []TreeEntry `json:"tree"`
	Truncated bool        `json:"truncated"`
}

// Commit is the part of a Git commit that names its tree.
type Commit struct {
	SHA  string `json:"sha"`
	Tree struct {
		SHA string `json:"sha"`
	} `json:"tree"`
}
