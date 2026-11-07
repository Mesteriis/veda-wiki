package mcp

import (
	"context"
	"net/http"

	"github.com/Mesteriis/veda-wiki/internal/mcp/client"
)

// writeHandlers forward the validated arguments as the request body. They
// are listed for owner keys only.
var writeHandlers = map[string]handler{
	"veda_create_source":           post("/api/v1/sources"),
	"veda_write_article":           writeArticle,
	"veda_edit_article":            editArticle,
	"veda_copy_article":            post("/api/v1/articles/copy"),
	"veda_reconcile_sources":       post("/api/v1/sources/reconcile"),
	"veda_configure_source_mirror": post("/api/v1/sources/mirrors"),
	"veda_cancel_move":             post("/api/v1/moves/cancel"),
	"veda_move_article":            post("/api/v1/moves"),
	"veda_resolve_conflict":        post("/api/v1/conflicts/resolve"),
	"veda_github_connect":          post("/api/v1/github/connections"),
	"veda_sync":                    post("/api/v1/github/sync"),
	"veda_publish_job":             post("/api/v1/github/publish-job"),
	"veda_cancel_bundle":           post("/api/v1/github/cancel-bundle"),
}

// post returns the response of POST route with the arguments as the body.
func post(route string) handler {
	return func(ctx context.Context, c *client.Client, args arguments) (any, error) {
		return c.Do(ctx, http.MethodPost, route, map[string]any(args))
	}
}

// writeArticle creates an article at revision 0 and updates it otherwise.
func writeArticle(ctx context.Context, c *client.Client, args arguments) (any, error) {
	method := http.MethodPut
	if args.integer("expected_revision") == 0 {
		method = http.MethodPost
	}
	raw, err := c.Do(ctx, method, "/api/v1/articles", map[string]any(args))
	if err != nil {
		return nil, err
	}
	return withoutText(raw)
}

// editArticle replaces one unique fragment of the current revision.
func editArticle(ctx context.Context, c *client.Client, args arguments) (any, error) {
	return c.Do(ctx, http.MethodPatch, "/api/v1/articles", map[string]any(args))
}

// withoutText drops the article text from a write response: the agent sent
// it and the SHA identifies it, so echoing it only spends tokens.
func withoutText(raw []byte) (object, error) {
	response, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	out := make(object, 0, len(response))
	for _, m := range response {
		if m.key != "text" {
			out = append(out, m)
		}
	}
	return out, nil
}
