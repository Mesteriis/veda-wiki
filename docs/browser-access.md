# Private browser reading

Use the profile button to sign in with the separately generated `reader.key`,
or with a source-scoped reader key created through the owner API. The shared
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

Successful login sets an HttpOnly, SameSite=Strict session cookie (Secure over
HTTPS). Sessions expire after eight hours and are held only in memory; restart
revokes them. Logout revokes the current session. The server applies a global
limit of ten login attempts per minute and at most 100 active sessions. To
revoke all sessions restart the service; credential rotation requires replacing
the reader key in the private data directory and restarting the service.

The GitHub dialog accepts the owner key for a separate administrator session;
the key is sent only over direct HTTPS or loopback HTTP and is not stored in the
browser. The administrator session is limited to GitHub account registration
and connection, and cannot authorize article writes or other owner APIs. It
uses its own HttpOnly, SameSite=Strict cookie, expires after eight hours, and
is revoked on logout or restart. The reader-only Unix socket does not expose
administration; a separately configured local bridge may forward the limited
administration routes. The interface offers no article
editing. Browser session and administration POST endpoints accept only
same-origin requests. This is an initial key-based reader mechanism, not a
multi-user account system. Revoking or expiring a scoped key prevents subsequent
private reads with its sessions; requests already in progress may finish. User
accounts, teams, and multi-user administration remain future work. On login,
expired sessions and sessions tied to revoked or expired scoped keys are removed
before applying the session limit.
