# App library

Open **Containers → Launch → App Templates** for one library of Compose starters.
Search descriptions or filter by source and category. Community cards use the
same layout as included starters, with upstream descriptions and source links.

- **DockTun**: included starters, available offline.
- **Lissy93 / Portainer templates**: a large community collection of container
  definitions and Compose stacks. Click **Load community catalogs** to fetch it.
  Its upstream catalog aggregates other maintainers; it is not a DockTun app list
  or a guarantee that every image supports every CPU architecture.
- **Docker Awesome Compose**: development examples. Some require repository
  source code or configuration files in addition to their Compose YAML.

Under **Collections & import from URL**, paste a public GitHub Portainer JSON
catalog URL to add its apps to the same library. You can also import a public
GitHub Compose YAML file directly. This does not download a Docker Hub image
containing templates; it reads the collection's public source JSON. Unraid XML
is not supported.

Selecting a template opens an editable draft. Read its setup comments, change
passwords, ports and host paths, and provide any required Environment variables.
An explicit empty variable default remains empty; missing defaults require a
value (an explicitly empty value in Environment is allowed). Environment defaults
for upstream Compose stacks are listed as comments for you to copy or override.
Upstream notes are plain text; no catalog HTML or scripts run in DockTun.

Container templates preserve supported runtime settings, including ports,
volumes, read-only flags, named volumes, labels, host networking and privilege
requirements. Unsupported settings, Windows entries and Swarm stacks display a
reason they cannot be imported. External network requirements remain explicit.
No tunnel provider is selected or enabled by this workflow.

Catalog revisions and imported stack files are pinned to Git commit IDs so a
selection cannot silently change to another template between browsing and import.
The catalog is cached for ten minutes. Only exact public GitHub hosts are read;
DockTun credentials are never forwarded to collection maintainers.

Before deployment, **Review storage** checks your edited Compose configuration.
Choose real Docker-host directories, such as `/volume1/docker/app/config` on a
Synology host, rather than leaving another maintainer's sample paths. Existing
readable files/directories are checked with read-only probes; missing binds are
blocked, not automatically created. Review does not prove that the application
UID has write permission. Named volumes can be created by Docker; external
volumes must already exist. Additional repository build assets and env files
must be supplied when needed. Review invalidates when the draft changes.

Sources: https://github.com/Lissy93/portainer-templates,
https://github.com/docker/awesome-compose,
https://docs.portainer.io/advanced/app-templates/format.

## Guided setup

Selecting a template opens **Set up this app** before the advanced YAML editor.
Login details, environment values, fixed host ports, timezone and container names
are editable as form fields. A shared Compose variable is shown once, so changing
a database password also updates every reference to that variable. **Generate
passwords** creates random passwords for password fields; API tokens, encryption
keys and password hashes still require the app's own setup instructions.

**Automatic storage for new app data** selects persistent Docker volumes for
suitable writable config/data mounts. Docker creates these at deployment; there
is no need to SSH into the host to create folders. Matching shared paths use one
volume and keep their read-only settings. Existing host files are not copied.
Media/library folders, file mounts, sockets, devices and advanced mount settings
stay explicit. You can choose existing host paths instead, including Synology
`/volume1/...` folders. Related parent/child paths need existing host storage to
preserve their directory layout.

**Apply settings & check app** updates the draft and runs the storage check.
The check also reports ports used by running Docker containers or duplicated
between services in the draft. Other host processes and advanced port ranges
are still checked by Docker during deployment. Changing the form disables
Deploy until those changes are applied. No app or tunnel starts until Deploy.

Some apps ask you to create the first administrator when you open their web UI.
DockTun only configures credentials declared by a template; it cannot infer an
app's private signup API. Upstream setup notes remain available in the form.
For advanced YAML aliases, expressions or multiline Environment values, the
advanced editor remains available. New and saved project `.env` files use mode
600 so generated login details are not written as world-readable Environment
files.

## Ports, dependencies, source files and compatibility

- **Find free host ports** keeps available ports and suggests alternatives for
  occupied ones. It checks the Docker daemon host, including non-Docker listeners,
  with a short-lived helper that binds sockets without serving traffic. TCP/UDP
  sharing and IPv6 bindings are preserved. Shared port variables update their
  references; recognized local public/base URLs update to the chosen port.
  Advanced ranges/protocols remain in the Compose editor. Ports are not reserved.
- **App dependencies** shows internal database/cache relationships. Matching
  username, password and database settings can stay linked, including literal
  connection URLs whose credentials must be URL-encoded. Links are visible and
  can be disabled. Detected dependencies can be added to startup order. Existing
  dependency options are preserved; health-based ordering is used when a health
  check is declared. Startup order alone does not prove an app is ready.
- **Supporting files & secrets** lists referenced configuration, environment and
  secret files. Fetch repository files imports a pinned public GitHub snapshot
  and the complete local build contexts within the Compose folder. Downloads
  are bounded to 128 files, 256 KiB per file, 4 MiB total, with four concurrent
  requests. Executable bits and binary assets are preserved. Symlinks, Git
  submodules, parent-folder escapes and projects beyond those limits require a
  complete checkout. Include/extends imports are not flattened. An upstream
  Environment example can seed the normal app settings form. Public sample
  secrets must be replaced; password files can be generated. App-specific keys
  and configuration syntax still follow the app's own instructions.
- **App checks** reports host CPU architecture, image/tag availability, Docker
  and project filesystem space, occupied host ports and app-user permissions.
  Local images or registry manifests are inspected without pulling whole images
  or starting apps. A missing image or known incompatible architecture blocks
  deployment; unavailable/private registry metadata is clearly marked unverified.
  Explicit foreign platforms require host emulation. Source builds are checked
  by Docker at deployment. Numeric UID:GID or PUID/PGID settings enable targeted
  filesystem permission checks. Missing user metadata remains a note; the check
  does not guess image users or automatically chmod host folders.

Files stay in the draft until deployment. Review stages their exact contents and
permissions in temporary storage; creation revalidates the draft, then saves
files inside the new project before Compose starts. Existing supporting files
with different contents are never overwritten. Private secret files use mode
600 and can have an explicit app UID/GID owner. This workflow does not enable an
exposure provider or publish an app automatically.

## Community template compatibility

Native Portainer PostgreSQL templates using the official `postgres:latest` or
version 18+ image receive the current `/var/lib/postgresql` mount destination
when imported as a new draft, unless the template explicitly sets `PGDATA`.
Older image versions keep their existing destination. This corrects an outdated
upstream template; it does not migrate existing databases. App preflight also
reports the old destination in manually supplied PostgreSQL 18+ Compose files.

## Testing community apps

`tests/template_review_integration.py` runs in a disposable nested Docker engine.
Enable `DOCKTUN_TEST_SETUP=1`, `DOCKTUN_TEST_NEXT=1` and
`DOCKTUN_TEST_COMMUNITY=1` for the guided setup, supporting files, credentials,
compatibility and live catalog regressions against the production image.

For a live app sample, retain that test engine with
`DOCKTUN_KEEP_TEST_ENGINE=1`, then pass its reported name and localhost URL as
`DOCKTUN_SAMPLE_ENGINE` and `DOCKTUN_SAMPLE_URL` to
`tests/template_catalog_smoke.py`. The script randomly selects one app from each
of five documented category pools; `DOCKTUN_SAMPLE_SEED` defaults to `20261030`.
It imports the actual pinned community entries, generates credentials, selects
free ports, uses managed storage, runs compatibility review, deploys, verifies
HTTP or database responses, and checks stored files and database records after
restart. Its JSON report records the source revision, image IDs and results.
This requires downloading upstream images. It does not certify every catalog
entry, complete app-specific onboarding, or enable any exposure provider.

Credential links require evidence of a shared database/cache account. Equal
sample passwords in unrelated apps do not create a link. Separate providers,
root accounts, and app administrator passwords remain independent. Explicit
Environment references still share their value; a variable reused by multiple
provider settings is called out for manual review rather than assigned
conflicting generated values.

Port discovery resolves edited bind-address variables in short and long Compose
syntax. Advanced ranges and protocols stay in the YAML editor. Imported native
Portainer `:ro`/`:rw` destination suffixes become explicit mount options; clock
and config files are not offered as empty data volumes. Automatically added
dependencies only wait for health when an active check is declared; `NONE`,
disabled checks, and unknown inherited checks use normal service startup.

`tests/template_edge_cases.py` verifies these draft cases against a retained
disposable test fixture. Set `DOCKTUN_EDGE_DEPLOY=1` to also deploy two independent
PostgreSQL accounts and clients, verify real SQL connections with different
generated passwords, and exercise both active and disabled health checks.
