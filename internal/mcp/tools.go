package mcp

import (
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/Mesteriis/veda-wiki/internal/mcp/client"
)

// ToolSpec is one MCP tool: its name, description, input schema, whether a
// read-only (reader key or socket) client may use it, and whether an editor
// client (owner key limited to articles) may use it besides the read-only tools.
type ToolSpec struct {
	Name, Description string
	Schema            json.RawMessage
	ReadOnly          bool
	Editor            bool
}

// Allowed reports whether client c may list and call the tool.
func (t ToolSpec) Allowed(c *client.Client) bool {
	if c.ReadOnly() {
		return t.ReadOnly
	}
	return !c.Editor() || t.ReadOnly || t.Editor
}

// Instructions are the server instructions sent at initialization: the
// safety rules, then how to create and maintain a knowledge repository.
//
//go:embed instructions.md
var Instructions string

// FormatGuide is the article formatting reference veda_format_guide returns.
//
//go:embed formatting.md
var FormatGuide string

// Property schemas shared by several tools.
const (
	nonEmpty    = `{"type":"string","minLength":1}`
	nonNegative = `{"type":"integer","minimum":0}`
	positive    = `{"type":"integer","minimum":1}`
	pageLimit   = `{"type":"integer","minimum":1,"maximum":100}`
	boolean     = `{"type":"boolean"}`
	confirmed   = `{"type":"boolean","const":true}`
	articleText = `{"type":"string","maxLength":1048576}`
	sha256Hex   = `{"type":"string","minLength":64,"maxLength":64}`
)

type property struct{ name, schema string }

func prop(name, schema string) property { return property{name, schema} }

func required(names ...string) []string { return names }

// closedObject builds a closed object schema with the properties in order.
func closedObject(requiredNames []string, properties ...property) json.RawMessage {
	var b strings.Builder
	b.WriteString(`{"type":"object","properties":{`)
	for i, p := range properties {
		if i > 0 {
			b.WriteByte(',')
		}
		name, _ := json.Marshal(p.name)
		b.Write(name)
		b.WriteByte(':')
		b.WriteString(p.schema)
	}
	b.WriteString(`},"required":`)
	if requiredNames == nil {
		requiredNames = []string{}
	}
	names, _ := json.Marshal(requiredNames)
	b.Write(names)
	b.WriteString(`,"additionalProperties":false}`)
	return json.RawMessage(b.String())
}

// Tools lists every tool: those of the former Python adapter in the order it
// declared them, then the knowledge tools.
var Tools = append([]ToolSpec{
	{
		Name:        "veda_sources",
		Description: "List sources visible to this client.",
		Schema:      closedObject(required()),
		ReadOnly:    true,
	},
	{
		Name: "veda_search",
		Description: "Search visible articles across sources using exact phrases or meaningful words. Generic container terms " +
			"include LXC and Docker; VPS, VDS and VSP are aliases. This is lexical search, not semantic inference. Returns " +
			"at most 20 ranked results with source_id, path and title; use veda_find to page further. LXC and Docker are " +
			"distinct: read source context and verify live state before acting.",
		Schema: closedObject(required("query"),
			prop("query", nonEmpty)),
		ReadOnly: true,
	},
	{
		Name: "veda_find",
		Description: "Find visible article paths with compact paginated lexical search. Optionally restrict to one source; read " +
			"results and their AGENTS.md context before acting. Results are not authorization or proof of live state.",
		Schema: closedObject(required("query"),
			prop("query", nonEmpty),
			prop("source_id", nonEmpty),
			prop("limit", pageLimit),
			prop("offset", nonNegative)),
		ReadOnly: true,
	},
	{
		Name:        "veda_read",
		Description: "Read the current article and its revision.",
		Schema: closedObject(required("source_id", "path"),
			prop("source_id", nonEmpty),
			prop("path", nonEmpty)),
		ReadOnly: true,
	},
	{
		Name: "veda_read_excerpt",
		Description: "Read a bounded portion of the current article. Offsets count Unicode code points; pass the first response's " +
			"sha256 as expected_sha256 for every continuation. A changed revision is refused.",
		Schema: closedObject(required("source_id", "path"),
			prop("source_id", nonEmpty),
			prop("path", nonEmpty),
			prop("offset", nonNegative),
			prop("limit", `{"type":"integer","minimum":1,"maximum":16000}`),
			prop("expected_sha256", sha256Hex)),
		ReadOnly: true,
	},
	{
		Name: "veda_prepare_update",
		Description: "Prepare a non-persistent proposed article update using the current revision, SHA and source status from one " +
			"snapshot. It does not save or publish the proposed text. Check coverage and sync status; unresolved import " +
			"conflicts must be resolved before a write. Reports pending writes and moves that block this article. Stale " +
			"revisions are refused.",
		Schema: closedObject(required("source_id", "path", "expected_revision", "text"),
			prop("source_id", nonEmpty),
			prop("path", nonEmpty),
			prop("expected_revision", positive),
			prop("text", articleText)),
		ReadOnly: true,
	},
	{
		Name: "veda_prepare_new_article",
		Description: "Prepare a non-persistent new Markdown article proposal and source status from one snapshot in a visible " +
			"source. Refuses an existing visible article; deleted history or concurrent writes can still cause a conflict. " +
			"It does not reserve the path, save the text, or approve publication.",
		Schema: closedObject(required("source_id", "path", "text"),
			prop("source_id", nonEmpty),
			prop("path", nonEmpty),
			prop("text", articleText)),
		ReadOnly: true,
	},
	{
		Name:        "veda_create_source",
		Description: "Create an explicit local source; GitHub connection is not implied.",
		Schema: closedObject(required("id", "title", "kind", "visibility"),
			prop("id", nonEmpty),
			prop("title", nonEmpty),
			prop("kind", `{"enum":["wiki","program"]}`),
			prop("visibility", `{"enum":["private","public"]}`)),
		ReadOnly: false,
	},
	{
		Name: "veda_write_article",
		Description: "Create (revision 0) or update an article with optimistic concurrency. A currently public source rejects " +
			"credential-like text, and GitHub sync blocks it for any repository; private local writes remain available. The" +
			" check can have false positives and cannot guarantee detection of secrets.",
		Schema: closedObject(required("source_id", "path", "text", "expected_revision"),
			prop("source_id", nonEmpty),
			prop("path", nonEmpty),
			prop("text", articleText),
			prop("expected_revision", nonNegative)),
		ReadOnly: false,
		Editor:   true,
	},
	{
		Name: "veda_copy_article",
		Description: "Copy a specific revision, retaining the source. To update an existing destination, supply its " +
			"expected_destination_revision; its history is preserved. Supported relative images are copied. With " +
			"include_linked, recursively copy linked articles to a new standalone local or connected GitHub destination; " +
			"conflicts are rejected. A GitHub group is queued as one commit and requires a complete pull for confirmation. " +
			"Otherwise linked articles must already exist at the destination with the same revision. Private-to-public " +
			"copying requires explicit user approval for the whole graph; any credential-like text is rejected for a public" +
			" destination. Inspect the outbox for remote confirmation.",
		Schema: closedObject(required("source_id", "source_path", "expected_revision", "destination_id", "destination_path"),
			prop("source_id", nonEmpty),
			prop("source_path", nonEmpty),
			prop("expected_revision", positive),
			prop("destination_id", nonEmpty),
			prop("destination_path", nonEmpty),
			prop("expected_destination_revision", nonNegative),
			prop("confirm_publication", boolean),
			prop("include_linked", boolean)),
		ReadOnly: false,
	},
	{
		Name: "veda_transfer_plan",
		Description: "Inspect the recursive relative article-link graph, destination matches and both source statuses in one " +
			"read-only snapshot. Check partial coverage, observation times, conflicts and sync state before proposing a " +
			"transfer. credential_like marks heuristic matches in each revision, with false positives and false negatives. " +
			"directory_index marks an index.md mapped to a destination README.md; review the mapped paths. This is not a " +
			"reservation or authorization. Images, HTML and publication approval are not validated by this plan.",
		Schema: closedObject(required("source_id", "source_path", "expected_revision", "destination_id", "destination_path"),
			prop("source_id", nonEmpty),
			prop("source_path", nonEmpty),
			prop("expected_revision", positive),
			prop("destination_id", nonEmpty),
			prop("destination_path", nonEmpty)),
		ReadOnly: true,
	},
	{
		Name: "veda_reconcile_sources",
		Description: "Reconcile a disconnected private source into a verified writable private GitHub source. Establish baselines " +
			"for matching articles, queue safe changes and leave divergent paths blocked. Never deletes content. Each call " +
			"commits at most limit articles; repeat if more is true and inspect blocked paths and the GitHub outbox.",
		Schema: closedObject(required("source_id", "destination_id"),
			prop("source_id", nonEmpty),
			prop("destination_id", nonEmpty),
			prop("limit", `{"type":"integer","minimum":1,"maximum":50}`)),
		ReadOnly: false,
	},
	{
		Name:        "veda_source_mirrors",
		Description: "Inspect owner-configured periodic private source reconciliation and its sanitized status.",
		Schema:      closedObject(required()),
		ReadOnly:    false,
	},
	{
		Name: "veda_configure_source_mirror",
		Description: "Enable or pause periodic private source reconciliation using an expected configuration revision. Pausing does " +
			"not cancel already queued GitHub writes. A new configuration uses expected_revision 0.",
		Schema: closedObject(required("source_id", "destination_id", "enabled", "expected_revision"),
			prop("source_id", nonEmpty),
			prop("destination_id", nonEmpty),
			prop("enabled", boolean),
			prop("expected_revision", nonNegative)),
		ReadOnly: false,
	},
	{
		Name:        "veda_moves",
		Description: "List move operations and their status; owner only.",
		Schema: closedObject(required(),
			prop("limit", pageLimit),
			prop("offset", nonNegative)),
		ReadOnly: false,
	},
	{
		Name:        "veda_cancel_move",
		Description: "Cancel a waiting move, preserving both copies; an already queued destination write may still finish.",
		Schema: closedObject(required("id", "confirm_cancel"),
			prop("id", nonEmpty),
			prop("confirm_cancel", confirmed)),
		ReadOnly: false,
	},
	{
		Name: "veda_move_article",
		Description: "Move a standalone local or synchronized writable GitHub article after explicit user instruction. With " +
			"include_linked, copy the linked graph to a standalone local or connected GitHub destination before removing " +
			"only the requested source article; other source articles remain. GitHub destination files are sent together " +
			"and must be observed before source deletion. Otherwise linked articles must already exist at the destination " +
			"with the same revision. Supported relative images are transferred. Local imported sources are unsupported. " +
			"Inspect veda_moves for completion. Private-to-public publication requires explicit user approval; any credential-like text is " +
			"rejected for a public destination.",
		Schema: closedObject(required("source_id", "source_path", "expected_revision", "destination_id", "destination_path", "confirm_move"),
			prop("source_id", nonEmpty),
			prop("source_path", nonEmpty),
			prop("expected_revision", positive),
			prop("destination_id", nonEmpty),
			prop("destination_path", nonEmpty),
			prop("confirm_move", confirmed),
			prop("confirm_publication", boolean),
			prop("include_linked", boolean)),
		ReadOnly: false,
	},
	{
		Name: "veda_context",
		Description: "Read root and ancestor AGENTS.md articles and source status for a target within one source. For connected " +
			"repositories inspect sync_state, sync_error, sync_verification, unconfirmed_write_jobs and " +
			"prepared_unconfirmed_write_jobs. Prepared jobs may have reached the remote; do not discard them or assume " +
			"their outcome. Status is not proof of live infrastructure.",
		Schema: closedObject(required("source_id", "path"),
			prop("source_id", nonEmpty),
			prop("path", nonEmpty)),
		ReadOnly: true,
	},
	{
		Name:        "veda_conflicts",
		Description: "List conflict summaries with pagination; supply source_id/path/index to inspect one incoming text.",
		Schema: closedObject(required(),
			prop("limit", pageLimit),
			prop("offset", nonNegative),
			prop("source_id", nonEmpty),
			prop("path", nonEmpty),
			prop("index", nonNegative)),
		ReadOnly: false,
	},
	{
		Name:        "veda_resolve_conflict",
		Description: "Resolve an import conflict against the current local revision, retaining history.",
		Schema: closedObject(required("source_id", "path", "index", "expected_revision", "resolution"),
			prop("source_id", nonEmpty),
			prop("path", nonEmpty),
			prop("index", nonNegative),
			prop("expected_revision", positive),
			prop("resolution", `{"enum":["local","upstream"]}`)),
		ReadOnly: false,
	},
	{
		Name:        "veda_github_status",
		Description: "Inspect explicit GitHub connections and the durable outbox; never returns credentials.",
		Schema: closedObject(required(),
			prop("limit", pageLimit),
			prop("offset", nonNegative)),
		ReadOnly: false,
	},
	{
		Name:        "veda_github_connect",
		Description: "Connect one repository anonymously or with an explicitly registered account; never guesses credentials.",
		Schema: closedObject(required("id", "repository"),
			prop("id", nonEmpty),
			prop("repository", nonEmpty),
			prop("account_id", nonEmpty),
			prop("branch", nonEmpty),
			prop("write", boolean),
			prop("adopt_existing", `{"type":"boolean","description":"Attach an existing disconnected private source to a private repository, preserving local differences as conflicts."}`)),
		ReadOnly: false,
	},
	{
		Name:        "veda_sync",
		Description: "Schedule an explicit retry for an existing GitHub connection; accepted work remains durable.",
		Schema: closedObject(required("id"),
			prop("id", nonEmpty)),
		ReadOnly: false,
	},
	{
		Name:        "veda_publish_job",
		Description: "Approve one private queued revision for publication only after explicit user instruction and review.",
		Schema: closedObject(required("job_id", "expected_revision", "confirm_publication"),
			prop("job_id", nonEmpty),
			prop("expected_revision", positive),
			prop("confirm_publication", confirmed)),
		ReadOnly: false,
	},
	{
		Name: "veda_cancel_bundle",
		Description: "Cancel an unprepared linked-article GitHub bundle. Local copies remain. A prepared bundle cannot be cancelled " +
			"until its remote outcome is resolved.",
		Schema: closedObject(required("job_id", "confirm_cancel"),
			prop("job_id", nonEmpty),
			prop("confirm_cancel", confirmed)),
		ReadOnly: false,
	},
	{
		Name: "veda_format_guide",
		Description: "Return the full article formatting reference: supported Markdown, alerts, collapsible blocks, code, " +
			"Mermaid diagrams, links, images, front matter and the warnings veda_write_article returns. Read it before " +
			"writing a page with anything beyond plain Markdown.",
		Schema:   closedObject(required()),
		ReadOnly: true,
	},
}, knowledgeTools...)
