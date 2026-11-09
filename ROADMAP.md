# Roadmap implementation and acceptance ledger

The roadmap and its audit baseline are merged into main. The first signed stable
release and physical-device acceptance are separate milestones.

| ID | Deliverable | Implementation | Verified acceptance / remaining gate |
|---|---|---|---|
| R0.1 | Audit baseline | #52 merged into main | Backend, frontend and container checks passed before merge |
| R0.2 | Accurate product documentation | Guides linked from README | Claims distinguish process readiness, data rollback and physical/live-account coverage |
| R0.3 | Historical published-version upgrade | Reproducible fixture | Genuine published 43a92aa image upgraded; changed password, data, mount and session continuity passed |
| R1.1 | Update preflight | Staged candidate ID, dependencies/storage and state checks | App/PostgreSQL update and login fixture passed; changed candidates require renewed review |
| R1.2 | Explicit readiness | Saved per-app HTTP readiness with replacement gate | HTTP failure/redirect/credential tests and existing Docker-health rollback fixtures passed |
| R1.3 | Database boundaries | Engine/major/series guard; binary version confirmation; containerd metadata fallback | Same-major database update passed; changed-major candidate refused before touching the working database |
| R1.4 | Durable operations | Transactional leases, worker ownership, interruptions, acknowledgement and secret-free mutation auditing | Restart and partial-recovery lease regressions; independent core/companion update fixtures passed |
| R2.1 | Backup levels | Project, database, quiesced named volume and selected instance state | Levels/limits documented; real archive/restore fixture passed |
| R2.2 | Database exports | PostgreSQL custom dumps and locking MySQL/MariaDB dumps; account/_FILE handling | Native PostgreSQL 17, MySQL 8.4 and MariaDB 11.4 rows restored to explicit empty targets |
| R2.3 | Encrypted recovery | Authenticated framed encryption, manifest checksums, new-volume extraction, remote read-back, schedules and conservative local retention | Wrong-key/tamper/truncation, paths, remote checksum, retention and one-use download tests passed |
| R2.4 | Clean-host drill | Import to fresh core; restore planning; recovery Compose preview and review mode | Disposable clean-instance login/data recovery passed; project paths/provider identities still require operator review |
| R3.1 | Channels | Stable/beta/edge policy and settings; explicit image override takes priority | Persistence tests passed; first reviewed signed release is a publication gate |
| R3.2 | Immutable installer | Versioned digest-pinned Compose plus metadata/checksum/signature bundle generation | Generator tests and empty-engine single-file candidate installation/recreation passed; real release artifact test gates promotion |
| R3.3 | Platform matrix | Explicit physical vs emulated coverage | Docker Desktop AMD64 acceptance; ARM64 build and emulated smoke passed. Physical NAS/ARM hardware not supplied |
| R3.4 | Signing/scanning gates | Exact-source validation, scan-before-sign, restricted publisher identity, signed release asset and promotion gates | Workflow lint passed; first signed/scanned publication must run under review. Legacy unsigned latest fails closed |
| R4.1 | App workspace | Existing container workspace now includes readiness, maintenance and incident history | Production frontend build and recovery/settings browser acceptance passed |
| R4.2 | Import preview | Read-only storage/project/network inspection without adoption | Uses read-only details/index paths; no folder or network creation |
| R4.3 | Template tiers | Maintained starter / community-review / unavailable tiers; existing deployment image/platform checks retained | All bundled Compose structure checks and real storage/.env fixture passed. Movie Night disabled until its external image exists; no blanket runtime-certification claim |
| R4.4 | Exposure safety | Preview, managed-hostname ownership, explicit public confirmation, Access-before-route and provider error handling | Local Cloudflare/Tailscale control-plane fixtures and failed/empty Access-policy regressions passed; live accounts not supplied |
| R5.1 | Durable alerts | Persisted tracker/outbox, event-triggered samples, bounded retries and delivery history | Queue restart/backoff/recovery and stable-name identity tests passed; at-least-once/coalescing limits documented |
| R5.2 | Support bundle | Metadata allowlist, exact preview/download checksum and expiry | Credential exclusion and exact-byte checksum checked in recovery fixture and browser |
| R5.3 | Incident timeline | Stable-name app maintenance and incident/delivery history | Events remain associated across healthy container image replacement; global history available in Settings |

## Additional failure points removed

Compose execution no longer inserts generated placeholder values ahead of a stack's
own `.env`. It no longer invents empty application-data directories or dummy config
files when a referenced source is missing. This restores the reviewed configuration
and prevents a deployment from appearing successful with replacement credentials
or empty data. Real stack environment/literal-value and missing-source tests cover
these regressions.

## Review and release gates

1. The audit baseline and roadmap are merged into main through PRs #52 and #53.
2. Publish the first reviewed release to transition legacy `latest` to signed stable.
   The release job must pass scans, signatures and the actual downloadable installer
   fixture before promoting a channel. No signed-release success is claimed locally.
3. Docker Desktop is the only user-provided physical environment. Obtain separate
   NAS/ARM hardware and live-provider acceptance before claiming those certifications.
4. Movie Night's missing external browser image remains disabled; do not substitute
   an unverified image or certify all community recipes from catalog metadata.

See [reliability/recovery](docs/reliability-and-recovery.md) and
[releases/platforms](docs/releases-and-platforms.md) for operating limits and the
[acceptance fixtures](tests/README.md) for reproduction.
