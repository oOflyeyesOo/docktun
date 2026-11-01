# App templates

Open **Containers → Deploy / Launch → App Templates**. A template fills the
editable Compose and optional `.env` fields. Nothing starts until you review
storage and deploy the stack. Exposure is a separate, optional choice.

The ten bundled examples live in `frontend/src/lib/compose-templates.mjs`.
They were added to DockTun in October 2026 and are maintained in this repository;
they are not automatically synchronized with an external catalog.

## Importing examples

**Browse Docker Awesome Compose** reads the public
[Docker Awesome Compose repository](https://github.com/docker/awesome-compose).
Its catalog is cached for ten minutes, and each selected file is pinned to the
catalog's Git commit. The upstream instructions remain linked beside each entry.
Docker describes these as development examples, not production-ready deployments.

**Import Compose** accepts a public GitHub `.yml`/`.yaml` file page or a
`raw.githubusercontent.com` URL. It imports that Compose file into the editor;
it does not execute a script or start a container. DockTun does not send account
tokens or Git credentials to these public sources.

This first importer handles individual Compose files, including those linked by
other GitHub collections. Portainer-style catalog JSON, private sources and
arbitrary web pages are not imported. Files needed for local builds, `include`,
`extends`, configs, secrets or service `env_file` entries are not downloaded with
a single Compose file. The storage review reports missing project files. Choose
an image-only example, adapt it to a published image, or supply the complete
source project before deploying it.
For `include`/`extends` projects, use the existing full-project workflow or flatten
the files into a single Compose document before using this first review flow.

## Storage review

1. Choose the stack name. Leave its folder blank to use DockTun's persistent
   projects folder, or select another folder mounted into DockTun.
2. Edit Compose and `.env` for your installation. Host bind paths must refer to
   real storage, such as `/volume1/media` on Synology; replace `/path/to/...`
   placeholders. Container destinations such as `/data` are separate paths.
3. Select **Review storage**. The table shows each source, container destination,
   requested access mode and check result. Host checks use short-lived helpers
   with read-only mounts, no network, no extra capabilities and a read-only root.
4. Correct missing paths or variables and review again. Directory/file existence
   and type are checked; the app image's UID/GID still controls its permissions.
5. Deploy. The backend repeats the review before starting the stack. Reviewed
   stacks prohibit Docker's implicit creation of missing host bind directories,
   including on later recreations. A path disappearing after review causes an
   error instead of creating an empty replacement directory.

Named volumes are managed by Docker and do not need an arbitrary host bind path.
Ordinary named volumes can be created by Compose; external volumes must already
exist. Missing bind-source folders must be created deliberately on the Docker host or
replaced with a suitable existing source. Read-only source folders cannot hold
the stack's editable Compose files.

Changing the stack name, folder, Compose or `.env` invalidates the previous UI
review. Reviewed creation requests use `review_storage: true`; direct API clients
can opt into the same review. Compose interpolation uses project variables and
does not inherit dashboard credentials from DockTun's process environment.

Selecting or importing an example does not make it part of DockTun's installation
and does not enable Cloudflare, Tailscale or WireGuard.

An imported template is an editable snapshot. Image updates do not replace its
Compose file with a newer upstream example or overwrite your storage choices.
Apply later Compose changes deliberately through the stack editor, which keeps
the normal file backups.
