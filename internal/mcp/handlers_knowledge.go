package mcp

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Mesteriis/veda-wiki/internal/mcp/client"
)

// MaxKnowledgeResultBytes bounds the REST answer a knowledge tool returns;
// the REST limits keep normal answers far below it.
const MaxKnowledgeResultBytes = 2 << 20

// errKnowledgeTooLarge refuses an answer above MaxKnowledgeResultBytes.
var errKnowledgeTooLarge = &toolRefusal{"VEDA knowledge result is too large; lower limit, depth, max_nodes or max_edges."}

// knowledgeHandlers call the knowledge REST routes; the service applies
// access, limits and status rules, and these only translate arguments.
var knowledgeHandlers = map[string]handler{
	"veda_entities":          knowledgeGet("/api/v1/knowledge/entities", renamed("query", "q")),
	"veda_entity":            knowledgeGet("/api/v1/knowledge/entity", nil),
	"veda_article_links":     knowledgeGet("/api/v1/knowledge/article", nil),
	"veda_graph":             knowledgeGet("/api/v1/knowledge/graph", graphArguments),
	"veda_graph_context":     knowledgeGet("/api/v1/knowledge/context", renamed("query", "q")),
	"veda_knowledge_status":  knowledgeGet("/api/v1/knowledge/status", nil),
	"veda_knowledge_reindex": knowledgeReindex,
}

// knowledgeGet returns the bounded response of GET route with the
// (translated) arguments as the query.
func knowledgeGet(route string, translate func(arguments) (arguments, error)) handler {
	return func(ctx context.Context, c *client.Client, args arguments) (any, error) {
		if translate != nil {
			var err error
			if args, err = translate(args); err != nil {
				return nil, err
			}
		}
		target := route
		if q := args.query(); q != "" {
			target += "?" + q
		}
		return bounded(c.Do(ctx, http.MethodGet, target, nil))
	}
}

func bounded(raw json.RawMessage, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxKnowledgeResultBytes {
		return nil, errKnowledgeTooLarge
	}
	return raw, nil
}

// renamed moves the argument from to the query parameter to.
func renamed(from, to string) func(arguments) (arguments, error) {
	return func(args arguments) (arguments, error) {
		out := arguments{}
		for name, value := range args {
			if name == from {
				name = to
			}
			out[name] = value
		}
		return out, nil
	}
}

// graphArguments requires either entity_id or both source_id and path.
func graphArguments(args arguments) (arguments, error) {
	_, entity := args["entity_id"]
	_, source := args["source_id"]
	_, path := args["path"]
	if entity == (source || path) || source != path {
		return nil, invalidArguments("provide either 'entity_id' or both 'source_id' and 'path'")
	}
	return args, nil
}

// knowledgeReindex checks that the targets match the scope, then posts the
// arguments as the strict REST body.
func knowledgeReindex(ctx context.Context, c *client.Client, args arguments) (any, error) {
	_, source := args["source_id"]
	_, path := args["path"]
	switch args.str("scope") {
	case "all":
		if source || path {
			return nil, invalidArguments("scope 'all' takes neither 'source_id' nor 'path'")
		}
	case "source":
		if !source || path {
			return nil, invalidArguments("scope 'source' takes 'source_id' and no 'path'")
		}
	case "article":
		if !source || !path {
			return nil, invalidArguments("scope 'article' takes 'source_id' and 'path'")
		}
	}
	return bounded(c.Do(ctx, http.MethodPost, "/api/v1/knowledge/reindex", map[string]any(args)))
}
