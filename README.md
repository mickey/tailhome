# <img src="docs/logo.svg" alt="tailhome logo" width="40" height="40"> tailhome

Stop maintaining dashboard cards. tailhome lists the apps on your [tailnet](https://tailscale.com/docs/concepts/tailnet), and the previews your coding agents publish.

![tailhome in light and dark themes, with grouped preview versions, app tiles, health dots and copyable connection details](docs/screenshot.png)

[![Release](https://img.shields.io/github/v/release/mickey/tailhome)](https://github.com/mickey/tailhome/releases) [![CI](https://github.com/mickey/tailhome/actions/workflows/ci.yml/badge.svg)](https://github.com/mickey/tailhome/actions/workflows/ci.yml) [![License: MIT](https://img.shields.io/github/license/mickey/tailhome)](LICENSE)

| You want | Start here |
|---|---|
| A start page for your NAS, home or team | Run [tailhome alone](#quick-start). No agent required. |
| An agent to give you preview links on your phone | Install [the skill alone](#previews-from-coding-agents). No tailhome server required. |
| Apps and previews in one place | Install both, then [enable preview discovery](docs/setup.md#optional-list-previews-in-tailhome). |

## Quick start

1. Install Docker on a machine you keep running. Enable MagicDNS and HTTPS Certificates in your [Tailscale DNS settings](https://console.tailscale.com/admin/dns), then run:

   ```sh
   docker run -d --name tailhome --restart unless-stopped \
     -v tailhome-data:/data ghcr.io/mickey/tailhome:latest
   docker logs -f tailhome
   ```

   Open the login link in the logs yourself to join your tailnet.

2. When the logs say `serving https://home.<tailnet>.ts.net`, open **http://home/** from a Tailscale-connected device. Use the name in the logs if different, such as `home-1`.

3. Empty page? Install Tailscale and sign in on your app's machine, then publish its local port:

   ```sh
   tailscale serve --bg --https=443 8080
   ```

   On Linux, use `sudo` or run `sudo tailscale set --operator=$USER` once. Press **refresh** to find the app.

Teammates and family only need Tailscale and [access allowed by your tailnet rules](https://tailscale.com/docs/features/access-control).

See [setup](docs/setup.md) for Compose, binaries, systemd and host requirements.

## Why tailhome

- No published host ports, Docker socket or reverse proxy to configure.
- Apps appear automatically with health dots. Copy SSH and Postgres connection details from Other.
- Install the page on your phone's home screen, or open an app with `home/go/<name>`.

For manual cards and widgets, use Homepage, Homarr, Dashy or Heimdall. You can [feed Homepage or Glance your app list](docs/customize.md#homepage-and-glance).

## Which apps appear

- [Tailscale Services](https://tailscale.com/docs/features/tailscale-services). Optional [OAuth](docs/setup.md#set-up-oauth) lists all Services with tags and descriptions.
- Apps on a device's tailnet name at HTTPS port 443.
- Agent previews on port 443 or [configured HTTPS ports](docs/setup.md#preview-ports).

For apps on other ports or in containers, see [publishing apps](docs/apps.md).

## Previews from coding agents

> [!TIP]
> Paste this into your coding agent:
>
> ```text
> Install the tailnet-preview skill (npx skills add mickey/tailhome --skill tailnet-preview -g), then show me this app on my phone.
> ```

You need Tailscale on Linux or macOS, MagicDNS, HTTPS Certificates, Python 3.9+ and the Tailscale CLI. To install yourself with Node 22.20+:

```sh
npx skills add mickey/tailhome --skill tailnet-preview -g
```

Restart your agent so it finds the skill, then ask: **Can I see this on my phone?** It asks which HTTPS ports it may use and leaves existing mappings alone.

Your phone needs Tailscale access. Keep the publishing machine awake and each app instance running. Ask the agent to remove previews when done; they never expire automatically and are never public.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/agents-dark.png">
  <img alt="A coding agent in a terminal publishes three versions of a landing page hero and lists their links; on a phone, home/ shows them at the top as Landing redesign, 3 versions, with badges A, B and C." src="docs/agents.png">
</picture>

See the [skill guide](skills/tailnet-preview/README.md) for other installs and [preview setup](docs/setup.md#preview-ports) to list previews in tailhome.

## Customize tiles

Use [`config/overrides.yaml`](docs/customize.md#overrides) to rename, group, reorder or hide tiles and change icons and links.

## Documentation

[Setup and configuration](docs/setup.md) · [Apps, previews and page controls](docs/apps.md) · [Customization and JSON endpoints](docs/customize.md) · [Troubleshooting](docs/troubleshooting.md) · [Contributing](CONTRIBUTING.md)

## Security and privacy

Everyone who can open tailhome sees the same catalog, including app and preview names, descriptions, addresses and status, on the page and JSON endpoints. App access rules do not hide this metadata. Hiding tiles does not restrict access.

Device discovery checks reachable devices, including other members' laptops. OAuth needs **Services: Read**; add **Auth Keys: Write** only to join without a login link.

tailhome has no analytics or telemetry. Logs can contain the login link: open it yourself and do not share it. Cookies are shared across HTTPS ports on the same hostname.

Review [network requests, icon privacy and Tailscale diagnostic logs](docs/setup.md#security-and-network-requests). Report vulnerabilities through [SECURITY.md](SECURITY.md).

## Is it any good?

[Yes.](https://news.ycombinator.com/item?id=3067434)

---

Tailscale is a trademark of Tailscale Inc. tailhome is not affiliated with or endorsed by Tailscale.
