<div align="center">

<img src="icons/icon-192.png" alt="" height="96">

# tailhome

The start page that builds itself from your tailnet.

A self-hosted launcher for your [Tailscale](https://tailscale.com/) network: published apps and coding agents' previews, at `http://home/`.

[![Release](https://img.shields.io/github/v/release/mickey/tailhome)](https://github.com/mickey/tailhome/releases)
[![Release date](https://img.shields.io/github/release-date/mickey/tailhome)](https://github.com/mickey/tailhome/releases)
[![License: MIT](https://img.shields.io/github/license/mickey/tailhome)](LICENSE)

</div>

Web apps get links; others, such as SSH or Postgres, get copyable addresses. tailhome runs as one container (Linux amd64, arm64 and armv7) or one binary (also macOS and Windows). It [joins your tailnet itself](https://tailscale.com/docs/features/tsnet), so it needs no Tailscale on the host and no API credentials.

It checks the devices it can reach, including other members' laptops and phones, and everyone who can open it sees the whole list. See [security and privacy](#security-and-privacy).

tailhome is pre-1.0 with one maintainer, who runs it daily. Settings and defaults may change between 0.x releases; read the [release notes](https://github.com/mickey/tailhome/releases) before upgrading.

<img alt="tailhome at home/ in two browser windows, the light theme in front of the dark one, with two versions of a coding agent's preview at the top, app tiles with health dots, and connection details for non-web Services." src="docs/screenshot.png">

<sub>Groups and descriptions shown here use overrides or OAuth. New installs group apps under Apps.</sub>

## Why tailhome

tailhome keeps your app list up to date by discovering what you publish on your tailnet with `tailscale serve`, [DockTail or TSDProxy](docs/apps.md#publish-container-apps). Your coding agents' work appears too: with the [tailnet-preview skill](#previews-from-coding-agents), an agent publishes what it built as a private HTTPS preview, and tailhome lists it above your apps. Ask for three versions and they share one heading, as A, B and C.

It is a launcher, not a dashboard. [Homepage](https://gethomepage.dev) and [Glance](https://github.com/glanceapp/glance) have widgets and feeds; tailhome can [feed them its app list](docs/customize.md#homepage-and-glance). It does not read Docker labels or look behind reverse proxies.

## Which apps appear

- A [Tailscale Service](https://tailscale.com/docs/features/tailscale-services) tailhome can reach. Optional [OAuth](docs/setup.md#set-up-oauth) also lists Services it cannot reach.
- A device app: an app at a device's own tailnet name on HTTPS port 443. Apps behind paths or a shared proxy need [separate publishing](docs/apps.md#app-on-another-port).
- A preview a coding agent published with `tailscale serve`, on port 443 or on a [preview port](docs/setup.md#preview-ports) you allow.

An app at `nas:8080` appears once you [publish it with one command](docs/apps.md#app-on-another-port). See [discovery rules, certificates and publishing tools](docs/apps.md).

## Quick start

Before you start:

- Enable MagicDNS and HTTPS Certificates in your [DNS settings](https://console.tailscale.com/admin/dns).
- On the Docker host, use Docker Compose 2.24.0 or later.
- On devices that open Services, use Tailscale 1.86 or later. On Linux before 1.94, also run `sudo tailscale set --accept-routes`.

Create [`compose.yaml`](compose.yaml). The `env_file` and `./config` lines are optional:
`.env` holds OAuth credentials, and `config/overrides.yaml` holds tile overrides. A missing overrides file is fine.

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

Open the login link in the logs to add tailhome to your tailnet. When the logs show `serving https://home.<tailnet>.ts.net`, open `http://home/` from a tailnet device. Keep the trailing slash. It redirects to that HTTPS address. Open a tile to launch an app; see [page controls, health dots and shortcuts](docs/apps.md#using-the-page).

> [!NOTE]
> Disable key expiry for the new `home` device on the [Machines](https://console.tailscale.com/admin/machines) page. If `home` is taken, use the address in the logs, such as `home-1`. Keep the state volume: deleting it creates a new node.

tailhome needs no host ports, Docker socket or extra capabilities. See the setup guide for [docker run, binaries, systemd and unattended login](docs/setup.md).

## Previews from coding agents

With the optional [tailnet-preview skill](skills/tailnet-preview/), a coding agent publishes the app it is working on with `tailscale serve`, hands you a private HTTPS link and later removes only what it published. tailhome lists each preview at the top of the page, with the title and description the agent chose.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/agents-dark.png">
  <img alt="A coding agent in a terminal publishes three versions of a landing page hero and lists their links; on a phone, home/ shows them at the top as Landing redesign, 3 versions, with badges A, B and C." src="docs/agents.png">
</picture>

The skill runs on the machine where the app runs, your laptop or a remote server, on Linux or macOS. It needs Python 3.9 or later and the Tailscale CLI, and works without tailhome. Install it there with Node 22.20 or later, and pick your agent when asked:

```sh
npx skills add mickey/tailhome --skill tailnet-preview -g
```

<details>
<summary>Install as a Claude Code, omp or Gemini CLI plugin</summary>

Claude Code:

```text
/plugin marketplace add mickey/tailhome
/plugin install tailnet-preview@tailhome
```

omp:

```sh
omp plugin marketplace add mickey/tailhome
omp plugin install tailnet-preview@tailhome
```

Gemini CLI:

```sh
gemini extensions install https://github.com/mickey/tailhome --ref main
```

</details>

<details>
<summary>Install without Node, or for an agent that npx skills doesn't list</summary>

Copy the skill where your agent finds skills. Many clients read `~/.agents/skills`; Claude Code reads `~/.claude/skills`:

```sh
git clone --depth 1 https://github.com/mickey/tailhome.git
mkdir -p ~/.agents/skills ~/.claude/skills
cp -R tailhome/skills/tailnet-preview ~/.agents/skills/
ln -s ~/.agents/skills/tailnet-preview ~/.claude/skills/tailnet-preview
```

The [installation reference](skills/tailnet-preview/references/serve.md#installation-and-updates) covers other clients, the Tailscale CLI on macOS and Python.

</details>

The first time, the agent asks which HTTPS ports it may use and suggests 8443-8452. Chat approval authorizes publishing; listing extra ports in tailhome needs the setting below. To skip the question next time, set `TAILNET_PREVIEW_PORTS` in the agent's environment.

> [!IMPORTANT]
> Extra preview ports are off by default. To list previews on that range, add this to tailhome's `environment:` in `compose.yaml`, then run `docker compose up -d`:
>
> ```yaml
> TAILHOME_PREVIEW_PORTS: 8443-8452
> ```
>
> Without it, agents can still publish previews on those ports and hand you links, but `home/` won't show them. Port 443 needs no extra-port setting: the skill's [preview card](docs/apps.md#preview-cards), a JSON description marked `preview: true`, identifies it as a preview.

A preview is private to your tailnet, not to you: anyone your Tailscale policy lets in can open it, and anyone who can open tailhome sees its title. Keep the app running and its machine awake. A reachable Serve listener can keep a preview listed after the app stops; unreachable previews disappear. Ask the agent to remove previews when done. See [preview privacy and lifetime](docs/apps.md#previews).

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
> Anyone who can open tailhome sees every listed app and preview, with its name, description and status, on the page and both JSON endpoints, including apps their own access rules block. Restrict access to tailhome accordingly. Hiding an entry hides it for everyone; it does not revoke access to the app.

- tailhome refreshes every five minutes by default and probes port 443 and your configured preview ports. Large tailnets can need several refreshes. See [device discovery](docs/apps.md#devices) and [preview checks](docs/apps.md#card-refresh-and-checks) for timing, requests and filtering. Use `TAILHOME_DEVICES=off` to stop device discovery.
- Icons load from `cdn.jsdelivr.net`. To avoid it, set `icon` overrides to your own URLs.
- Like other Tailscale nodes, tailhome uploads diagnostic logs to Tailscale. Set `TS_NO_LOGS_NO_SUPPORT=true` to stop uploads.
- Previews on different ports of one device share its cookies. Don't publish an untrusted preview on a device whose other apps keep you logged in.

Report vulnerabilities through [SECURITY.md](SECURITY.md). To contribute, see [CONTRIBUTING.md](CONTRIBUTING.md). Licensed under [MIT](LICENSE).
