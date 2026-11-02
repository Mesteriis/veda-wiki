# Storage and migration

VEDA defaults to its bounded JSON store. A stopped instance can be migrated
explicitly to the SQLite backend with:

```sh
./veda migrate-sqlite --data /path/to/private-data
```

The command takes the same exclusive lock as the server and refuses an active
writer, an existing migration artifact, an invalid store, or an already
migrated store. It saves `state.before-sqlite.json`, writes `state.sqlite` as
separate records in a transaction, reopens and validates the snapshot, syncs the
directory and finally switches `state.json` to a version 14 SQLite marker. Old
binaries reject that marker. Migration does not touch blobs or credentials.
Creating a reader user or team upgrades the logical database to version 15;
older binaries reject the new records. The version 14 SQLite marker remains the
backend selector and must be kept together with `state.sqlite`.

Do not automatically delete migration artifacts after an interrupted migration.
If the command reports an error, inspect the marker and artifacts before doing
anything else: the marker may already have been switched when a directory sync
failed. Preserve a full private backup and use a compatible VEDA binary to
inspect the active store. `state.before-sqlite.json` is a snapshot from the
migration point; restoring it later discards all subsequent changes. Never roll
back by simply starting an older binary or by replacing the marker with the old
JSON file. Plan and verify a restore first.

SQLite uses extra synchronous rollback-journal transactions and a private data
directory; temporary SQL data stays in memory. Its file is `state.sqlite` and
may have a journal while the service runs. Backups must capture a consistent
SQLite database together with all VEDA blobs and private data; the simplest
procedure is to stop VEDA and back up the entire data directory. A raw copy of
only the database while the service is running is not a supported backup. Test
restores independently before relying on a backup.

An atomic filesystem snapshot must include the database, any rollback journal,
and the rest of the data directory at the same point in time. One isolated
Proxmox/PBS snapshot taken while a synthetic VEDA instance was writing restored
and started successfully. This single exercise does not establish safety for
other storage systems or all failure modes; validate the complete backup and
restore path for the deployment in use.

The SQLite backend removes the 64 MiB whole-state JSON limit and updates
individual records within an atomic transaction. It still loads the complete
logical state into memory and serializes records during commits. It is not a
fully indexed or retention-managed database; large histories and collections
need further performance and recovery validation. Migration is opt-in; verify
the backup and recovery procedure separately for each deployment.
