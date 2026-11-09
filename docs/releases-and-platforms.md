# Reviewed releases and supported platforms

## Channel policy

- Stable (`latest`, with a `stable` alias): promoted only by a published reviewed
  non-prerelease after exact-source validation, upgrade/recovery fixtures, scan,
  signature verification and installer attachment.
- Beta (`beta`): the same gates for a published prerelease.
- Edge (`edge`): validated main builds. Both architecture scans and signature
  verification must pass before the workflow moves the channel tag. The build
  first pushes a commit-specific tag for scanning.
- `sha-<commit>` and release version tags identify source builds. The release job
  refuses to overwrite a release version. No automatic public release is requested
  or performed by this roadmap branch.

Before the first release under this policy, `latest` is a legacy main-build tag
and may be unsigned. Official update verification fails closed rather than treating
that legacy image as a newly reviewed stable release. Publish a reviewed signed
release to complete the transition. Do not bypass that check by default.

Settings chooses a channel without installing it. An explicit `DOCKTUN_UPDATE_IMAGE`
override takes precedence, including for offline/local development. Custom/local
image overrides use operator-managed trust. Official repository references must
resolve to an immutable registry digest and pass Cosign verification restricted
to this repository's release/publish workflow identity and GitHub's OIDC issuer.
The build compiles pinned Cosign source with a patched Go toolchain and HTML parser. Docker socket
access remains equivalent to broad Docker-host administration.

The release workflow emits a single `compose.yml` using prebuilt immutable image
digests, plus `release.json`, `SHA256SUMS` and a Sigstore bundle for the Compose
asset. Save it in a new install directory and run `docker compose up -d`; no source
checkout, Dockerfile or compiler is needed. Release installers deliberately pin
`DOCKTUN_UPDATE_IMAGE` to their reviewed digest. Download a newer reviewed installer
or explicitly remove that pin to use the channel selector. Preserve existing
volumes and bind locations when changing an existing installation.

Image signatures are checked with the [Sigstore verification flow](https://docs.sigstore.dev/cosign/verifying/verify/).
The workflow scans fixed HIGH/CRITICAL findings as a promotion gate; an unfixed
finding is still a risk and can remain in the generated report/SBOM. Scanning is
not proof of an absence of vulnerabilities. Runtime updates enforce publisher
identity, not an independent fresh scan of every third-party application image.

## Acceptance matrix

| Environment | What is covered | What is not established |
|---|---|---|
| Windows Docker Desktop, Linux AMD64 engine | Local build, Compose install, login/data continuity, app/core/companion rollback, PostgreSQL/MySQL/MariaDB recovery, provider API/control-plane fixtures, browser acceptance | Physical Linux/NAS kernel behavior; live provider accounts |
| Linux AMD64 CI | Same disposable fixtures in Validate and release gates | Consumer NAS permission/storage integration |
| ARM64 cross-build/emulation | Multi-platform release build; local ARM64 emulation smoke passed on 2026-11-08 | Physical ARM64 performance, kernel networking, devices |
| Synology/QNAP/other NAS | Deployment target with explicit path, privilege and port review | Physical acceptance pending; not runtime-certified here |
| Live Cloudflare/Tailscale/WireGuard | Existing authenticated integration paths and local API/control-plane tests | No live-account credential or external-reachability acceptance supplied |

The user currently provides Docker Desktop only. Physical-device and live-account
checks remain named acceptance gates, not silently marked complete.

## Template tiers

Bundled starters are maintained recipes, community entries require review, and
unavailable recipes stay disabled. Every deployment still runs the existing
image/platform, file, port and storage checks. A catalog title or successful import
is not runtime certification. Structural Compose checks cover all bundled recipes;
fixture evidence is scoped to the exact scenarios exercised. Movie Night remains
disabled until its browser companion is actually published and verified; DockTun
does not substitute an unrelated image just to make its button work.
