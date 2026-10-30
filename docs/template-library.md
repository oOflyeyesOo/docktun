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
