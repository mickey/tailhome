<div align="center">

<img src="icons/icon-192.png" alt="" height="96">

# tailhome

The start page that builds itself from your tailnet.

[![Release](https://img.shields.io/github/v/release/mickey/tailhome)](https://github.com/mickey/tailhome/releases)
[![Release date](https://img.shields.io/github/release-date/mickey/tailhome)](https://github.com/mickey/tailhome/releases)
[![License: MIT](https://img.shields.io/github/license/mickey/tailhome)](LICENSE)

</div>

tailhome finds your [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) and apps on their own tailnet devices, then links to them at `http://home/`. You do not maintain a separate app list.

It runs as one container (about 12 MB, Linux amd64, arm64 and armv7) or one binary (also macOS and Windows). It [joins your tailnet itself](https://tailscale.com/docs/features/tsnet), so it needs no Tailscale on the host and no API credentials.

tailhome is pre-1.0 with one maintainer, who runs it daily. Settings and defaults may change between 0.x releases; read the [release notes](https://github.com/mickey/tailhome/releases) before upgrading.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshot-dark.png">
  <img alt="tailhome at home/, with app tiles, health dots and connection details for non-web Services." src="docs/screenshot.png">
</picture>

<sub>Groups and descriptions shown here use overrides or OAuth. New installs group apps under Apps.</sub>

## Why tailhome

tailhome is a launcher, not a dashboard. It has no widgets, feeds or server stats; [Homepage](https://gethomepage.dev) and [Glance](https://github.com/glanceapp/glance) do those well, and tailhome can [feed them its app list](docs/customize.md#homepage-and-glance). A Service published with `tailscale serve`, [DockTail or TSDProxy](docs/apps.md#publish-container-apps) appears on the next refresh. tailhome does not scan port ranges, read Docker labels or look behind reverse proxies.

## Which apps appear

- A Tailscale Service tailhome can reach, or every Service with an OAuth client.
- An app on its own tailnet device that answers HTTPS on port 443.

An app at `nas:8080` appears once you publish it one of these two ways. See [discovery rules, certificates and publishing tools](docs/apps.md).

## Quick start

Before you start:

- Enable MagicDNS and HTTPS Certificates in your [DNS settings](https://console.tailscale.com/admin/dns).
- On the Docker host, use Docker Compose 2.24.0 or later.
- On devices that open Services, use Tailscale 1.86 or later.

Create [`compose.yaml`](compose.yaml). The `env_file` and `./config` lines are optional:
`.env` holds OAuth credentials, and `config/overrides.yaml` holds tile overrides.

```yaml
services:
  tailhome:
    image: ghcr.io/mickey/tailhome:latest
    env_file: [{ path: .env, required: false }] # optional OAuth client, see docs/setup.md
    environment:
      TZ: Etc/UTC # your time zone, for the "updated" time
      TAILHOME_OVERRIDES: /config/overrides.yaml
    volumes:
      - tailhome-data:/data
      - ./config:/config:ro # put overrides.yaml here
    restart: unless-stopped

volumes:
  tailhome-data:
```

```sh
mkdir -p config
docker compose up -d
docker compose logs -f
```

Open the login link in the logs to add tailhome to your tailnet. When the logs show `serving https://home.<tailnet>.ts.net`, open `http://home/` from a tailnet device, keeping the trailing slash. Open a tile to launch an app; see [page controls, health dots and shortcuts](docs/apps.md#using-the-page).

> [!NOTE]
> Disable key expiry for the new `home` device on the [Machines](https://console.tailscale.com/admin/machines) page. If `home` is taken, use the address in the logs, such as `home-1`. Keep the state volume: deleting it creates a new node.

tailhome needs no host ports, Docker socket or extra capabilities. For [docker run, binaries, systemd and unattended login](docs/setup.md), see the setup guide.

## Customize tiles

Put `overrides.yaml` in `config/` to change labels, groups, icons and links, or hide an entry:

```yaml
jellyfin:
  group: Media
  description: Films and TV
postgres:
  hidden: true
```

See [all fields](docs/customize.md) and [`overrides.example.yaml`](overrides.example.yaml). For groups and descriptions from the admin console, [set up an OAuth client](docs/setup.md#set-up-oauth).

## Documentation

- [Set up tailhome](docs/setup.md)
- [Apps and discovery](docs/apps.md)
- [Customize tiles](docs/customize.md)
- [Troubleshooting](docs/troubleshooting.md)

## Security and privacy

> [!WARNING]
> Anyone who can open tailhome sees every listed app and its status, on the page and both JSON endpoints, including apps their own access rules block. Restrict access to tailhome accordingly. Hiding an entry removes it from these lists; it does not change access to the app.

- Every five minutes by default, tailhome sends `GET /` on port 443 to every device it may reach in your tailnet, other members' laptops and phones included, and checks each listed app. Requests name `tailhome` in their User-Agent. Set `TAILHOME_DEVICE_TAGS` to limit which devices it tries, or `TAILHOME_DEVICES=off`.
- Icons load from `cdn.jsdelivr.net`. To avoid it, set `icon` overrides to your own URLs.
- Like other Tailscale nodes, tailhome uploads diagnostic logs to Tailscale. Set `TS_NO_LOGS_NO_SUPPORT=true` to stop uploads.

Report vulnerabilities through [SECURITY.md](SECURITY.md). To contribute, see [CONTRIBUTING.md](CONTRIBUTING.md). Licensed under [MIT](LICENSE).
