# Private browser reading

Use the profile button to sign in with a named user and password, the separately
generated `reader.key`, or a source-scoped reader key created through the owner
API. Named users receive the union of the source scopes of their teams; a user
without teams sees no private sources. Team membership and source permissions
are checked on each request. Password changes and disabling a user invalidate
all of that user's existing sessions; changing team permissions takes effect on
subsequent requests. Requests already in progress may finish with the previous
scope. Users can change their own password from the profile after confirming the
current password; doing so signs out all their sessions. Users are read-only and
cannot manage accounts, GitHub or articles. The shared
key grants current content of all private sources. A scoped key grants current
content only from its explicitly authorized sources; its validity and scope are
checked on every request. Neither grants writing or administration. Keep keys
private and never put them in URLs.

Login is allowed only over a direct HTTPS connection to the private listener,
or HTTP on loopback from the local machine. For direct HTTPS configure both
`--tls-cert` and `--tls-key` on the server, and include the intended hostname
in `--allowed-hosts`. Use a certificate trusted by the client. The public
listener remains public-only and ignores reader credentials and sessions.
The application does not trust forwarded headers: TLS terminated at a proxy
without TLS to VEDA is not accepted for remote browser login.

An additional direct HTTPS listener can be enabled with `--https-listen`
alongside the existing loopback HTTP listener. Set `--tls-cert`, `--tls-key`
and include the HTTPS hostname or IP in `--allowed-hosts`. In this mode the
ordinary HTTP listener must remain on loopback; the separate HTTPS port handles
private browser sessions directly. Certificates must be trusted by clients and
cover the chosen address. The public-only listener is unchanged.

Successful login sets an HttpOnly, SameSite=Strict session cookie (Secure over
HTTPS). Sessions expire after eight hours and are held only in memory; restart
revokes them. Logout revokes the current session. The server applies a global
limit of ten login attempts per minute and at most 100 active sessions. To
revoke all sessions restart the service; credential rotation requires replacing
the reader key in the private data directory and restarting the service.

An authenticated named administrator can open GitHub and access management
using the existing reader session. Role removal, disablement, password changes,
logout and expiry invalidate this access; ordinary readers and reader keys do
not gain administration. The GitHub dialog also accepts the owner key for a
separate administrator session;
the key is sent only over direct HTTPS or loopback HTTP and is not stored in the
browser. The administrator session is limited to GitHub account registration
and connection, and cannot authorize article writes or other owner APIs. It
uses its own HttpOnly, SameSite=Strict cookie, expires after eight hours, and
is revoked on logout or restart. The reader-only Unix socket does not expose
administration; a separately configured local bridge may forward the limited
administration routes. The interface offers no article
editing. Browser session and administration POST endpoints accept only
same-origin requests. Revoking or expiring a scoped key prevents subsequent
private reads with its sessions; requests already in progress may finish. On
login, expired and invalidated sessions are removed before applying the limit.

The owner creates teams, assigns their source scopes, creates users with an
initial password, assigns teams, disables users and resets passwords in the
separate access-administration session. The initial password must be conveyed to
the intended person securely; the user can then change it. Passwords must be
10–128 Unicode characters and at most 512 UTF-8 bytes. VEDA stores salted
Argon2id hashes (19 MiB, two iterations, one lane), never plaintext passwords;
administrative listings return neither hashes nor salts. Password confirmation
and login share the ten-attempts-per-minute limiter. This is a local reader
account system; MFA, SSO, account recovery, account deletion and audit history
are not implemented. Do not expose it remotely without a suitable HTTPS and
network-access design.

Settings provide a separate owner-authenticated access-administration session
for listing sources and issuing or revoking source-scoped reader keys. Its
key list can also change a key's source scope without showing or rotating the
bearer. Existing sessions use the new scope on their next request. A concurrent
change or an expired key requires the administrator to refresh and review;
expired keys must be reissued. The access-administration cookie cannot authorize
GitHub administration, private reading, or article
writes; a GitHub-only cookie or a non-administrator reader cookie cannot manage
keys. Login and mutations
require the same HTTPS/loopback and same-origin protections described above.
A new key is shown once and remains only in the current page's memory; save it
before closing the dialog. If creation fails or the response is lost, inspect
the key list before retrying: the server may have created the key even if its
value was not received. A key with an unknown value should be revoked and a
new ID created. Revocation stops subsequent reads and sessions associated with
that key; in-flight requests may finish. Issuing a key upgrades the store to
version 4 or newer; verify backup and rollback compatibility before use. These
are separate agent credentials and do not establish OS isolation. Creating a
team or user upgrades the logical store to version 15: older binaries reject it.
The SQLite marker remains version 14 and must stay paired with its database.
Back up and verify a compatible restore before creating accounts or rolling back.

## Administrator first setup

An administrator is a named user with access to all private sources and to the
browser GitHub and access-administration routes. Those sessions do
not grant the owner REST API or article writes. Changing or disabling the user
or changing its administrator role invalidates existing user sessions.

The first administrator must be provisioned through the root-authenticated local
Unix socket; the endpoint is unavailable to other peers and browser origins.
The operation returns a random, single-use setup token valid for 24 hours; only
its salted Argon2 hash is stored. The owner enters the token and a new password
in the GitHub dialog under “Первый вход — установить пароль”, then logs in.
Passwords must contain 10–128 Unicode characters. The local operator may issue
a new setup token for the same pending account, invalidating the previous one;
an already configured administrator cannot be replaced by this operation.
Transport the token privately and do not put it in URLs, logs or command
arguments. This changes the logical store format to version 20: older binaries
refuse it. A compatible backup and restore plan are required before setup.
Workstation bridges must forward the reader session to the private listener
and must not use a trusted reader socket as anonymous browser access. The
browser reader session grants administration only to a named administrator;
independent key-based administrator sessions remain available.

## Creating an administrator from a terminal

On Linux, with the VEDA service running and its root-authenticated local socket
configured, run as root:

```sh
veda createsuperuser --username USER --socket /path/to/reader.sock
```

The command requires a terminal, prompts twice with echo disabled and never
accepts a password as a command-line argument or environment variable. Omit
`--username` to be prompted. Passwords must contain 10–128 Unicode characters.
An existing active account is not overwritten. If the specified account is an
administrator awaiting its initial setup, the command completes it and revokes
its setup token. The user can then sign in to the GitHub administration dialog
with their username and password. Root access to the socket is required; a
browser, non-root socket peer, public listener or origin-bearing request cannot
create an administrator. Back up the v20 store and retain a compatible binary.
