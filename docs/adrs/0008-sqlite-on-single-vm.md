# SQLite as the first database

Status: Accepted

The backend runs as one process on one nanops VM, and the first version has a small number of users. Thus the backend uses SQLite (WAL mode) in a file under `/opt/health-assistant/`, not PostgreSQL. This removes a database container and keeps deployment and backup simple. Backup must use a consistent copy, not a copy of the live file. Meal photos stay in S3 (see [0007](0007-store-compressed-meal-photos.md)).

Trade-offs: SQLite permits one writer at a time and only one backend instance. Keep SQL portable and keep all database access behind one repository layer, so that a move to PostgreSQL stays possible. Reconsider when the backend needs more than one instance or write contention becomes visible.

Correction (2026-11-22): nanops-backup already finds SQLite files under `/opt/<project>/shared/` and takes a consistent `sqlite3 .backup` snapshot with `PRAGMA quick_check`. Thus the project needs no `pre-backup` hook for the database; a hook would back it up twice. See the technical design, section 10.6.
