tools.json is the golden list of server identity and tools, in owner and
reader mode, produced by scripts/dump_mcp_tools.py from the former Python
adapter (the server module in mcp/, as of commit 4c595a2; removed afterwards).
The Go server must keep matching it. To compare a build:

    python3 scripts/dump_mcp_tools.py --veda ./veda --sort --output /tmp/go.json

and compare /tmp/go.json with tools.json after sorting its tools by name; the
Go server lists tools sorted, the old adapter in definition order (the
golden file's order, checked by TestToolTableOrderMatchesPythonAdapter).

`veda_format_guide` (a reader read returning the embedded formatting
reference) and the seven knowledge tools (five reader reads, two owner tools)
came later and are appended after the adapter's tools in both lists, in
tool-table order.
They were taken from a `--veda` dump; every earlier entry is unchanged
(TestToolSchemasAreByteIdentical compares each schema byte for byte).

Later tools sit next to their relatives instead, in tool-table order:
`veda_outline`, `veda_changes`, `veda_stale` and `veda_check_source` (reader) after
`veda_read_excerpt`, and `veda_edit_article`
(owner and editor) after `veda_write_article`. These entries changed on
purpose: `veda_read` gained `if_sha256`, `veda_search` and `veda_find`
describe the `match` field, descriptions were shortened, and the server
instructions describe the new tools.

To regenerate the original from history: `git show 4c595a2:mcp/<module>.py >
/tmp/server.py` (see `git ls-tree 4c595a2 mcp/`), then `dump_mcp_tools.py --adapter /tmp/server.py` (needs the
official mcp Python SDK; see scripts/requirements-checks.txt).
