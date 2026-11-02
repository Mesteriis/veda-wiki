#!/usr/bin/env bash
# GitHub-hosted ephemeral runner only; installs the actual release unit and
# leaves its state to the runner's lifecycle. Never use on a persistent host.
set -euo pipefail
if [[ "${GITHUB_ACTIONS:-}" != true || "${RUNNER_ENVIRONMENT:-}" != github-hosted || -z "${RUNNER_TEMP:-}" || -z "${GITHUB_WORKSPACE:-}" ]]; then
  echo 'This test requires a disposable GitHub-hosted runner.' >&2
  exit 1
fi
umask 022
archive_dir="$RUNNER_TEMP/veda-artifacts"
(cd "$archive_dir" && sha256sum -c SHA256SUMS)
if getent passwd veda >/dev/null || getent group veda >/dev/null; then
  echo 'VEDA user or group already exists; refusing to overwrite.' >&2
  exit 1
fi
for target in /usr/local/bin/veda /etc/systemd/system/veda.service /var/lib/veda; do
  if [[ -e "$target" || -L "$target" ]]; then
    echo 'VEDA installation already exists; refusing to overwrite.' >&2
    exit 1
  fi
done
python3 - <<'PY'
import socket
for port in (8080, 8081):
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', port))
PY
tar -xzf "$archive_dir/veda-ci-linux-amd64.tar.gz" -C "$RUNNER_TEMP"
cd "$RUNNER_TEMP/veda-ci-linux-amd64"
sudo useradd --system --user-group --home-dir /var/lib/veda --no-create-home --shell /usr/sbin/nologin veda
sudo install -m 0755 veda /usr/local/bin/veda
sudo install -m 0644 deploy/systemd/veda.service /etc/systemd/system/veda.service
sudo systemctl daemon-reload
sudo systemctl enable --now veda.service
check_service() {
  python3 - "$1" <<'PY'
import json, sys, time, urllib.error, urllib.request
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
for attempt in range(100):
    try:
        with opener.open('http://127.0.0.1:8080/health', timeout=1) as response:
            assert json.load(response)['pages'] == int(sys.argv[1])
        break
    except (OSError, urllib.error.URLError):
        time.sleep(.1)
else:
    raise RuntimeError('VEDA did not become ready')
with opener.open('http://127.0.0.1:8081/api/v1/articles', timeout=2) as response:
    assert json.load(response) == []
with opener.open('http://127.0.0.1:8080/', timeout=2) as response:
    assert b'/assets/' in response.read()
PY
  sudo systemctl is-active --quiet veda.service
  sudo systemctl is-enabled --quiet veda.service
  sudo test "$(sudo stat -c '%U:%G:%a' /var/lib/veda)" = 'veda:veda:700'
  for key in owner.key reader.key; do
    sudo test "$(sudo stat -c '%U:%G:%a' "/var/lib/veda/$key")" = 'veda:veda:600'
  done
}
check_service 0
mcp_dir="$(mktemp -d /tmp/veda-ci-mcp-XXXXXXXX)"
chmod 0755 "$mcp_dir"
python3 -m venv "$mcp_dir/venv"
"$mcp_dir/venv/bin/pip" install -r mcp/requirements.txt
cp mcp/server.py "$mcp_dir/server.py"
cp "$GITHUB_WORKSPACE/scripts/smoke_installed_mcp.py" "$mcp_dir/smoke_installed_mcp.py"
sudo -u veda "$mcp_dir/venv/bin/python" "$mcp_dir/smoke_installed_mcp.py" --adapter "$mcp_dir/server.py" --data /var/lib/veda --create
sudo systemctl restart veda.service
check_service 1
sudo -u veda "$mcp_dir/venv/bin/python" "$mcp_dir/smoke_installed_mcp.py" --adapter "$mcp_dir/server.py" --data /var/lib/veda
echo 'PASS: archive installed on fresh systemd runner; MCP, private isolation and restart verified.'
