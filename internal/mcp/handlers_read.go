package mcp

import (
	"context"
	"net/http"
	"net/url"

	"github.com/Mesteriis/veda-wiki/internal/mcp/client"
)

// searchLimit bounds veda_search; veda_find pages further.
const searchLimit = "20"

var readHandlers = map[string]handler{
	"veda_sources":        wrapped("sources", "/api/v1/sources"),
	"veda_search":         search,
	"veda_find":           withQuery(findQuery, "/api/v1/search"),
	"veda_read":           read,
	"veda_read_excerpt":   withQuery(arguments.query, "/api/v1/articles/excerpt"),
	"veda_outline":        withQuery(arguments.query, "/api/v1/articles/outline"),
	"veda_context":        withQuery(arguments.query, "/api/v1/context"),
	"veda_transfer_plan":  post("/api/v1/articles/transfer-plan"),
	"veda_source_mirrors": wrapped("mirrors", "/api/v1/sources/mirrors"),
	"veda_moves":          withQuery(arguments.query, "/api/v1/moves"),
	"veda_conflicts":      withQuery(arguments.query, "/api/v1/conflicts"),
	"veda_github_status":  githubStatus,
	"veda_format_guide":   formatGuide,
}

// formatGuide returns the embedded formatting reference; it reads no data.
func formatGuide(context.Context, *client.Client, arguments) (any, error) {
	return map[string]string{"guide": FormatGuide}, nil
}

// wrapped returns {key: <GET route response>}.
func wrapped(key, route string) handler {
	return func(ctx context.Context, c *client.Client, _ arguments) (any, error) {
		raw, err := c.Do(ctx, http.MethodGet, route, nil)
		if err != nil {
			return nil, err
		}
		return object{{key, raw}}, nil
	}
}

// withQuery returns the response of GET route?<query of the arguments>,
// omitting an empty query.
func withQuery(query func(arguments) string, route string) handler {
	return func(ctx context.Context, c *client.Client, args arguments) (any, error) {
		target := route
		if q := query(args); q != "" {
			target += "?" + q
		}
		return c.Do(ctx, http.MethodGet, target, nil)
	}
}

func search(ctx context.Context, c *client.Client, args arguments) (any, error) {
	return c.Do(ctx, http.MethodGet, "/api/v1/search?"+url.Values{"q": {args.str("query")}, "limit": {searchLimit}}.Encode(), nil)
}

// findQuery renames query to q and passes source_id, limit and offset.
func findQuery(args arguments) string {
	params := arguments{"q": args["query"]}
	for _, name := range []string{"source_id", "limit", "offset"} {
		if value, ok := args[name]; ok {
			params[name] = value
		}
	}
	return params.query()
}

// read returns the current revision, or only its number and digest when it
// still has the sha256 the caller passed as if_sha256.
func read(ctx context.Context, c *client.Client, args arguments) (any, error) {
	article, err := readLatest(ctx, c, args.str("source_id"), args.str("path"))
	if err != nil || args.str("if_sha256") == "" {
		return article, err
	}
	sha, err := article.get("sha256")
	if err != nil {
		return nil, err
	}
	if current, ok := pyValue(sha).(string); !ok || current != args.str("if_sha256") {
		return article, nil
	}
	revision, err := article.get("revision")
	if err != nil {
		return nil, err
	}
	return object{{"source_id", args.str("source_id")}, {"path", args.str("path")}, {"revision", revision},
		{"sha256", sha}, {"unchanged", true}}, nil
}

// readLatest returns {source_id, path, **latest version} of an article.
func readLatest(ctx context.Context, c *client.Client, sourceID, path string) (object, error) {
	raw, err := c.Do(ctx, http.MethodGet, "/api/v1/articles?"+url.Values{"source_id": {sourceID}, "path": {path}}.Encode(), nil)
	if err != nil {
		return nil, err
	}
	article, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	result := object{}
	for _, key := range []string{"source_id", "path"} {
		value, err := article.get(key)
		if err != nil {
			return nil, err
		}
		result.set(key, value)
	}
	versionsRaw, err := article.get("versions")
	if err != nil {
		return nil, err
	}
	versions, err := decodeArray(versionsRaw)
	if err != nil || len(versions) == 0 {
		return nil, errInvalid
	}
	latest, err := decodeObject(versions[len(versions)-1])
	if err != nil {
		return nil, err
	}
	for _, m := range latest {
		result.set(m.key, m.value)
	}
	return result, nil
}

func githubStatus(ctx context.Context, c *client.Client, args arguments) (any, error) {
	connections, err := c.Do(ctx, http.MethodGet, "/api/v1/github/connections", nil)
	if err != nil {
		return nil, err
	}
	outbox, err := withQuery(arguments.query, "/api/v1/github/outbox")(ctx, c, args)
	if err != nil {
		return nil, err
	}
	return object{{"connections", connections}, {"outbox", outbox}}, nil
}
