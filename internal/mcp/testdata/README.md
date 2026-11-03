tools.json is the golden list of server identity and tools, in owner and
reader mode, produced by scripts/dump_mcp_tools.py from the former Python
adapter (mcp/server.py, last changed before commit 4c595a2, removed afterwards).
The Go server must keep matching it. To compare a build:

    python3 scripts/dump_mcp_tools.py --veda ./veda --sort --output /tmp/go.json

and compare /tmp/go.json with tools.json after sorting its tools by name; the
Go server lists tools sorted, the old adapter in definition order (the
golden file's order, checked by TestToolTableOrderMatchesPythonAdapter).

To regenerate the original from history: `git show 4c595a2:mcp/server.py >
/tmp/server.py`, then `dump_mcp_tools.py --adapter /tmp/server.py` (needs the
official mcp Python SDK; see scripts/requirements-checks.txt).
