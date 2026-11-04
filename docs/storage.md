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
logical state into memory and serializes records during commits. Migration is
opt-in; verify the backup and recovery procedure separately for each
deployment.

## Layout from store format 21

The SQLite file has three tables. `records` holds one row per source, article
(history without texts), job, move, key and so on. `revisions` holds one row per
stored article revision (its text and bound images). `revision_assets`
mirrors those images, one row per revision image, so that loading can check
them against the revision rows; blob collection does not read it and uses the
in-memory set of referenced images instead. An article write
inserts the new revision row and deletes pruned ones in the same transaction
and leaves other articles' rows untouched. Loading requires every revision of
an article record to have exactly one row whose text matches its SHA, and fails
otherwise. Stores below format 21 keep the texts inside the article records and
have neither table; a store whose tables do not match its format is rejected.
The marker in `state.json` stays `{"version":14,"backend":"sqlite"}`.

Revisions are addressed by number. Numbers only grow and are never reused, so
the stored history can start above 1; `expected_revision` compares with the
number of the newest revision.

## Retention

Every commit that writes an article keeps its three newest revisions
(`domain.KeepRevisions`, not configurable) plus the revisions that a record can
still read: the revision of a non-final outbox job and the first revision of
each article in its linked bundle, the source revision of a non-final move and
the first stored revision of its destination, the revision another article's
reconcile base names, and, for an article of a connected source, the newest
revision whose hash is its upstream hash. A protected revision is pruned at the
next write of that article, or the next full commit, after the protection ends.
Retention also runs once over the whole store during `migrate-revisions`.

## Blob collection

Images are immutable files named by SHA-256 under `blobs/`. A blob is removed
only when no record references it (source images, stored and published
revisions, pending jobs and moves all count) and its file is more than 24
hours old. `WriteBlobs` refreshes the file time even for an existing blob, so an
image written for a commit that has not happened yet stays protected. The sweep
runs under the store write lock, at most once every five minutes after a
successful commit or at start, never in a failed or read-only store, and logs
only counts. It is paused while any `state.before-revisions.*` file exists.
`migrate-revisions` does not remove blobs; the first sweep after the backup is
deleted does.

## Converting to format 21

This binary refuses to open a store below format 21 with
`store format vN requires "veda migrate-revisions"`. Convert a stopped
instance, after a PBS snapshot of the stopped service, with:

```sh
./veda migrate-revisions --data /path/to/private-data
./veda migrate-revisions --verify --data /path/to/private-data
```

The command takes the writer lock and refuses an active writer, an unclean
stop (a `state.sqlite-journal` or `-wal` file; see below), an existing
`state.before-revisions.*` backup (except the cases below), an invalid,
inconsistent or already converted store, and revisions that reference missing
or damaged images (it prints only their counts). Nothing
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

After a successful conversion the command writes
`state.before-revisions.done`: the counts and the SHA-256 of the converted
store file, nothing else.

`--verify` rebuilds the expected store from the backup and runs the same
checks against the current store. It opens the store and the backup
read-only and writes no store file; it takes `writer.lock`, the lock file the
service creates, so no writer changes the store meanwhile. Its only write is
`state.before-revisions.done`, and only on OK when the record is missing: a
migration that stopped between its commit and the record. A rerun of
`migrate-revisions` on such a store does the same and prints
`migration verified; completion recorded`. It prints the counts
and `verify: OK` or `verify: FAIL` (non-zero exit). Only a passing verify means
the migration is good; a service that starts proves nothing.

Once the service has written to the converted store, the store no longer
matches the completion record. A rerun or `--verify` then reports that the
migration completed and the store changed since, and does not advise a
restore: restoring the backup would discard those writes. Remove the
`state.before-revisions.*` files once you no longer need the rollback point.

Restore only when there is no `state.before-revisions.done` and verify fails,
or when you deliberately accept losing every change made since the migration
(restoring discards all of them):

1. stop the VEDA service and all clients; the service must stay stopped until
   the restore is complete;
2. delete any `state.sqlite-journal`, `state.sqlite-wal` and `state.sqlite-shm`
   left by the converted store (SQLite only); SQLite would otherwise apply them
   to the restored file;
3. copy `state.before-revisions.sqlite` over `state.sqlite` (SQLite only) and
   `state.before-revisions.json` over `state.json`;
4. delete all `state.before-revisions.*` files; otherwise migrate-revisions
   refuses to run again.

Or restore the PBS snapshot, with the same loss. If a run failed before
converting the store, rerun it: a complete backup identical to the unconverted
store is reused. An incomplete backup, or one taken before later writes to the
unconverted store, must be deleted before rerunning; the command says which.
If the store has an SQLite journal from a hard stop, nothing may write to it
until it is recovered: stop all clients, start the current binary once with no
clients connected (opening the store rolls the journal back; on an unconverted
store it then refuses to start, which is expected), stop it, then rerun
migrate-revisions or `--verify`. The previous binary is not used for this: it
refuses a converted store, and on an unconverted one it would serve clients.
Blob collection stays paused while any `state.before-revisions.*` file exists,
including the completion record. Binaries before format 21 refuse a converted
store; rolling back means restoring the backup or the snapshot.

## Measured conversion cost

`scripts/bench_migrate_revisions.py` builds a format 20 SQLite store of 550
articles with 10 revisions each (about 3 KiB per text) and 150 PNG images of
about 150 KiB, 120 listed by the source, and converts it. Synthetic, this Mac,
2026-11-04 (release build, local SSD, one run of the script, repeated twice with
the same results to within 10%):

| Measure | Result |
| --- | --- |
| `migrate-revisions` | 0.6 s (550 articles, 1650 revisions kept, 3850 removed) |
| `migrate-revisions --verify` | 0.2 s |
| `state.sqlite` before / after | 18,362,368 / 18,382,848 bytes |
| Blob files before / after | 150 / 150 (the migration removes no blob) |
| Service start to `/health` 200 on the converted store | about 55 ms |
| One blob collection sweeping 30 unreferenced files, under the write lock | about 9 ms |
| One collection sweeping nothing | about 3 ms |

The file size barely changes because SQLite does not return freed pages: the
3850 removed texts leave free pages inside the file, which later revisions
reuse. Run `VACUUM` only on a stopped, backed-up instance if the size matters.
These numbers say nothing about the production store, its disk or the PBS
snapshot and restore times. No production store has been converted: the
migration has not been performed on LXC161 or anywhere outside tests and this
benchmark.

## Rollback and the PBS snapshot

Take a PBS snapshot of the stopped service before running `migrate-revisions`.
That snapshot, or the `state.before-revisions.*` copy described above, is the
only way back: binaries before format 21 refuse a converted store. Restoring
either discards every change made after the snapshot or the migration.

Rolling back to the build before translation was removed (commit `81dfeb2`) has
two effects on a store written since the upgrade. Translations held by SQLite
articles that were not rewritten since the upgrade reappear: as current if the
original is unchanged, and publicly if they had been approved. A waiting move of
a program article may switch to `source_changed` under that build, because the
moves were rewritten without the translation fingerprint. Roll back only when
no program article has a waiting move, or accept that outcome.
