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
