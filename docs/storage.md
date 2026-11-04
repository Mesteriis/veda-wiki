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
Transfers containing links resolved through directory `README.md` upgrade the
logical database to version 16, which older binaries reject. Rolling back to a
pre-v16 binary requires a separately verified compatible restore and can lose
changes made after that backup.
Mapping a source directory `index.md` to a destination `README.md` during a
linked transfer upgrades the logical store to v17. Pre-v17 binaries reject it.
The synthetic `scripts/smoke_directory_restore.py` check runs the actual
binary, copies a linked directory fixture, migrates to SQLite and starts a
copy of the stopped store. Pass `--index` to exercise the v17 mapping. Both
variants passed against the installed Linux binary in an isolated temporary
directory. This does not prove the production backup or restore process.

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

From store format 21 the SQLite file keeps each article revision as its own row
in a `revisions` table, with its images listed in `revision_assets`; the
article record holds the history without texts. An article write inserts the
new revision row and deletes pruned ones in the same transaction and leaves
the other rows untouched. Loading requires every revision of an article record
to have exactly one row whose text matches its SHA, and fails otherwise. Stores
below format 21 keep the texts inside the article records and have neither
table; a store whose tables do not match its format is rejected.

This binary refuses to open a store below format 21 with
`store format vN requires "veda migrate-revisions"`. Convert a stopped
instance, after a PBS snapshot of the stopped service, with:

```sh
./veda migrate-revisions --data /path/to/private-data
./veda migrate-revisions --verify --data /path/to/private-data
```

The command takes the writer lock and refuses an active writer, an unclean
stop (a `state.sqlite-journal` or `-wal` file: start and stop the previous
binary once, then retry), an existing `state.before-revisions.*` backup, an
invalid, inconsistent or already converted store, and revisions that
reference missing or damaged images (it prints only their counts). Nothing
changes before a complete backup exists: each file is copied to
`state.before-revisions.<x>.tmp`, synced, reread, compared and renamed, the
SQLite copy first and `state.before-revisions.json` (the marker for SQLite,
the whole store for JSON) last. The backup must then load as the store. Partial
`.tmp` copies from an interrupted run are removed by the next run. The command
then binds images to revisions, keeps the three newest revisions of each
article plus protected ones, sets format 21 and, on SQLite, moves the texts
into revision rows, all in one transaction. It rereads the store and checks
SQLite integrity, `Validate`, the kept revisions and their texts byte for byte,
and every referenced image. It prints only counts.

`--verify` rebuilds the expected store from the backup, without writing, and
runs the same checks against the current store; it prints the counts and
`verify: OK` or `verify: FAIL` (non-zero exit). Only a passing verify means the
migration is good; a service that starts proves nothing. Run it before the
service writes again: a store changed after the migration fails it.

If the command fails after the backup, or verify fails, restore before starting
any binary:

1. stop VEDA;
2. copy `state.before-revisions.sqlite` over `state.sqlite` (SQLite only) and
   `state.before-revisions.json` over `state.json`;
3. delete both `state.before-revisions.*` files; otherwise migrate-revisions
   refuses to run again.

Or restore the PBS snapshot. If the command reports an incomplete backup and an
unmodified store, delete the `state.before-revisions.*` files and run it again.
Blob collection stays paused while any `state.before-revisions.*` file exists;
after a passing verify, delete the backup once a rollback is no longer needed.
Binaries before format 21 refuse a converted store; rolling back means
restoring the backup or the snapshot.
