"""Opt-in public GitHub read acceptance. No GitHub credentials and no remote writes."""
import argparse
import json
import subprocess
import tempfile
import time
from pathlib import Path
from urllib.request import Request, build_opener, ProxyHandler, HTTPRedirectHandler

from smoke import ROOT, free_port, start


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--repository", required=True, help="Explicit public owner/repository")
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="veda-github-live-") as tmp:
        data = Path(tmp) / "data"
        private, public = free_port(), free_port()
        process = start(data, private, public)
        opener = build_opener(ProxyHandler({}), NoRedirect())
        key = (data / "owner.key").read_text()

        def request(route, body=None, owner=True):
            req = Request(f"http://127.0.0.1:{private if owner else public}" + route,
                          data=None if body is None else json.dumps(body).encode(),
                          headers={"Content-Type": "application/json"})
            if owner:
                req.add_header("Authorization", "Bearer " + key)
            with opener.open(req, timeout=35) as response:
                return json.load(response)

        try:
            request("/api/v1/github/connections", {"id": "acceptance", "repository": args.repository, "write": False})
            deadline = time.monotonic() + 150
            while time.monotonic() < deadline:
                connection = request("/api/v1/github/connections")[0]
                if connection["state"] == "ready":
                    break
                if connection["state"] in ("error", "skipped"):
                    raise RuntimeError("GitHub acceptance: " + connection["state"] + " " + connection.get("last_error", ""))
                time.sleep(0.25)
            else:
                raise RuntimeError("GitHub import did not complete")
            visible = request("/api/state", owner=False)["view"]
            assert visible["Count"] > 0
            assert connection["verification"] == "verified" and connection["private"] is False
            assert connection["write"] is False
            assert request("/api/v1/github/outbox")["total"] == 0
            print(json.dumps({"status": "passed", "public_articles": visible["Count"],
                              "repository": args.repository, "git_commit": connection["last_pull"],
                              "github_credentials": False, "remote_writes": False}))
        finally:
            process.terminate()
            process.wait(timeout=10)


if __name__ == "__main__":
    main()
