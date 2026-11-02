# Linux service

The example [systemd unit](../deploy/systemd/veda.service) starts one VEDA
process under a dedicated `veda` user. It binds both listeners to loopback and
lets systemd create `/var/lib/veda` with mode 0700. It does not configure a
reader socket, TLS, remote access, GitHub accounts, a translation model, or
backups. Review and adapt the unit for the host before installing it.

For a Debian-family host with systemd, build the binary on a trusted build host
for the target architecture, or build an archive on a trusted build host:

```sh
python3 scripts/package_release.py --version dev --arch amd64
```

Choose `arm64` for Linux ARM64. The archive contains the embedded-interface
binary, MCP adapter and SSH launcher, pinned Python requirements, documentation
and example unit;
it contains no application data or credentials. Verify the printed SHA-256
when transferring the archive. Extract it on the destination and run the
following from the extracted directory. Commands in the documentation that
build VEDA or run source tests require a separate source checkout:

```sh
sudo useradd --system --user-group --home-dir /var/lib/veda --no-create-home --shell /usr/sbin/nologin veda
sudo install -m 0755 veda /usr/local/bin/veda
sudo install -m 0644 deploy/systemd/veda.service /etc/systemd/system/veda.service
sudo systemctl daemon-reload
sudo systemctl enable --now veda.service
sudo systemctl status veda.service
```

Use an existing dedicated service account instead of creating a duplicate.
Do not reuse another service's data directory or run multiple writers against
it. The generated keys and application state belong to the service account;
do not copy them into the unit, logs, command-line arguments or a repository.
The unit does not make a private browser login available from another machine.
Remote access needs an independently designed TLS, hostname and network boundary;
see [browser access](browser-access.md).

The default new store uses JSON. An administrator can stop the service and
perform the optional SQLite migration as the service user, after checking the
backup and rollback requirements in [storage](storage.md):

```sh
sudo systemctl stop veda.service
sudo -u veda /usr/local/bin/veda migrate-sqlite --data /var/lib/veda
sudo systemctl start veda.service
```

If migration fails, inspect the reported state and recovery instructions before
starting or retrying; do not remove migration artifacts blindly.

Set up and test backups before relying on the service. Keep the executable and
state versions compatible during restore; the example unit is not an automated
backup or recovery mechanism. Successful CI runs also provide temporary
`veda-linux-ci` artifacts for amd64 and arm64, with a SHA-256 manifest. These
are CI builds, not signed releases, and expire under GitHub Actions retention.
Verify the manifest before installing. CI builds both architectures, but ARM64
execution has not yet been verified. This repository does not yet publish
prebuilt releases.
