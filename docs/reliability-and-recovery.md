# Reliability and recovery

## Updates and database connectivity

Container image updates recreate the original Docker definition: mounts, environment,
entrypoint, ports, restart policy, network membership and aliases stay attached.
They do not generate a new one-service stack or change database credentials.
Review the staged candidate before installing; if its image ID changes, review again.
A Docker healthcheck or configured HTTP readiness endpoint gates replacement.
Choose a readiness endpoint that actually checks the database/login dependency.
Without either, a stable process is the limit of verification.

Known PostgreSQL, MySQL and MariaDB image families have a version boundary check.
PostgreSQL major changes and MySQL/MariaDB release-series changes require a separate
backup and migration. Custom database images and application schema migrations
still need operator review. Reverting an image cannot undo database writes.

Compose deployments use the project's real `.env` and Compose's interpolation.
DockTun does not inject placeholder credentials, URLs or variable values. Missing
project configuration/data paths stop deployment instead of creating dummy config
or empty replacement data. Template setup may prepare resources explicitly during
its review flow; execution never invents missing secret/config file contents.

Core/companion updates, app image updates and recovery jobs share durable leases.
They cannot overlap with a conflicting journaled operation. An interrupted or
partially recovered job retains its lease until the operator inspects its listed
resources and acknowledges recovery. Independent update workers are inspected
across core restarts. Do not run concurrent Compose deploys, external Docker
commands or other administrators' maintenance against resources being backed up.
This is not a host-wide transaction across every possible Docker client.

## Four backup levels

1. Project archive: Compose, `.env`, supporting files; excludes volume data.
2. Database export: PostgreSQL custom-format `pg_dump`; MySQL/MariaDB logical
   dumps use table locking for transactional and non-transactional tables.
   MySQL/MariaDB exports may block writers and require sufficient dump privileges.
   Exports use the selected account and existing container password or `_FILE`
   configuration; no separate database password is requested by DockTun.
3. Named volumes: all running attached containers must be explicitly selected
   for stop/resume. Volumes are rechecked for live writers before capture.
   Use logical database exports when downtime is unacceptable.
4. DockTun instance: consistent SQLite snapshot, account records/signing key,
   selected notification/mesh/Cloudflare configuration. Active login sessions,
   download tickets and operation leases are removed from the recovered snapshot.
   Other projects, application volumes, WireGuard and Tailscale device state
   require their own explicit selections. Environment-only provider credentials
   and arbitrary files outside the selected resources are not an instance backup.

All recovery archives use scrypt-derived AES-256-GCM with authenticated frames,
authenticated end-of-stream and object SHA-256 checksums. Keep the 12–1024 byte
recovery passphrase separately from the host. Losing it makes recovery impossible.
Plaintext snapshots temporarily occupy the protected data/recovery/tmp directory;
startup removes interrupted spools. Backups/restores are bounded to 16 GiB of
selected objects and require additional temporary space. Recovery is presently
intended for small and medium installations, not multi-terabyte disk imaging.
Archive creation, integrity verification, remote read-back and successful restore
have separate timestamps. A checksum is not evidence of application recovery.

## Recovery drill

Import the encrypted archive into a fresh DockTun instance. Supply the recovery
key, inspect the manifest and map only the desired objects. Volume restoration
creates new labelled volumes and rejects existing destinations; tar extraction
is confined to an empty new volume and rejects escaping paths/special devices.
Database restoration requires an explicitly prepared empty database of the same
engine and major/release series. Major migration is a separate operation.
Project recovery requires a new name/folder. Existing data is never an overwrite
option in the planner. Only restore trusted database archives, using an account
restricted to the prepared target when possible: native SQL is executable database
content, not a sandbox. SQL restore failures may leave partial data in the chosen empty target;
inspect it and prepare another empty target before retrying.

The plan includes a Compose preview for the new instance volume. Stop the original
core before switching to the recovered instance. Review the image reference,
project mount/path mappings, provider identities and notification destinations.
The recovery Compose binds to loopback and starts in review mode. Confirm review,
remove explicit recovery/provider-disable overrides, then restart deliberately.
Provider reconciliation, notifications and schedules are paused in review mode.
Other explicit authenticated actions remain available to the reviewing operator.
The original instance/project catalog does not automatically rewrite host paths.
Do not run both cores against the same database volume.

Large archive downloads use a one-use HttpOnly cookie scoped to that archive,
with a 60-second start window. No login/recovery token is placed in the URL.
If a download is interrupted, request a new download from the signed-in dashboard.

## Remote storage, schedules and retention

Configure an HTTPS/WebDAV collection supporting PUT and GET. Objects are encrypted
before upload and namespaced per installation. A successful PUT is followed by a
full download/hash comparison. Redirects and embedded URL credentials are refused.
For authorization, mount a file containing the Authorization header value and set
`DOCKTUN_BACKUP_REMOTE_AUTH_FILE` to that path. For schedules, mount a separately
managed recovery-key file and set `DOCKTUN_BACKUP_KEY_FILE`. The key is not saved
in the database/settings. Scheduled container selections use stable names.
Keep these files and their Compose mounts separate from exported instance data.

Retention respects both minimum age and minimum count. It preserves the newest
successfully restored archive and never retires a local-only/unuploaded archive
automatically. It only deletes regular UUID archive files in DockTun's own store.
Remote deletion is separate and is not automated. Run a fresh-host drill regularly.

## Exposure, alerts and support

The access preview reads the application, target and provider configuration.
Read permissions do not prove DNS/tunnel/Access write permission or live edge
reachability. Conflicting managed hostname ownership is rejected. Requested
Cloudflare Access policy changes must succeed before a new route is published.
Empty allowlists do not silently become everyone, failed provider responses are
errors, and unrelated existing Access policies require manual review. Provider
partial failures are reported; inspect accepted routes/DNS before retrying.

Container events request fresh samples; periodic sampling covers event-stream
interruptions. Very brief stop/start cycles can still coalesce. Stable container
names correlate maintenance and alert history across image replacements. Incidents
and monitor state are committed together. Failed delivery retries back off to one
hour, with eight failures before manual retry; recovery supersedes stale outage
retries. Delivery is at-least-once: a crash after an upstream acceptance can cause
a duplicate. External notification systems are not a distributed transaction.

Support exports are metadata only. Preview and downloaded bytes share a checksum.
Environment values, raw logs, credentials, app files, database rows and host mount
source paths are excluded. Names, IDs, network names and mount destinations remain
visible for diagnosis; review the preview before sharing. Artifacts expire after
one hour and are cleaned when another preview is generated.
