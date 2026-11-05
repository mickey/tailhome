# Apps and discovery

[Back to the README](../README.md).

## What qualifies

| App | Requirement |
|---|---|
| [Tailscale Service](https://tailscale.com/docs/features/tailscale-services) | Visible to tailhome's node. OAuth lists every Service in the tailnet. |
| App on its own tailnet device | HTTPS on port 443 or a [configured preview port](setup.md#preview-ports), with a verified certificate or [tag exception](#devices-with-their-own-certificate). |

Allow tailhome's node to reach each app in your Tailscale policy. With OAuth, grant access to `tag:tailhome` ([setup](setup.md#set-up-oauth)). Apps on arbitrary ports, under subpaths or behind shared proxies need publishing first; a `url` override cannot add an app.

### App on another port

Run this on the app's device, replacing `8080` with its local port:

```sh
tailscale serve --bg --https=443 8080
```

Open `https://<device>.<tailnet>.ts.net/`. Keep the device awake and the app running; the mapping survives restarts. See [Serve permissions](https://tailscale.com/docs/features/tailscale-serve).

For several apps, publish a separate [Service](https://tailscale.com/docs/features/tailscale-services) for each, or use [preview ports](#previews) for development. Paths on one port share a tile.

HTTPS device and Service names appear in public [Certificate Transparency logs](https://tailscale.com/docs/how-to/set-up-https-certificates).

### Services

A Service becomes a tile when it advertises port 443 or 80, has a `url` or `web: true` override, or answers HTTP on its first TCP port. Other Services appear under Other; `web: false` keeps one there unless it has a `url` override.

### Devices

tailhome discovers devices on port 443 and [configured preview ports](setup.md#preview-ports). It ignores shared-in devices, hidden entries, and public-share nodes (`tag:tailhome-share`). Use `TAILHOME_DEVICE_TAGS` to filter devices by tag or `TAILHOME_DEVICES=off` to disable device discovery.

### Devices with their own certificate

Fix unverifiable certificates when possible. For a dedicated NAS or router, tag the device and set `TAILHOME_DEVICE_INSECURE_TLS_TAGS=tag:nas` on tailhome. Do not tag personal laptops merely to bypass checks: [tagging changes identity and access](https://tailscale.com/docs/features/tags).

The tile and browser may warn about the certificate. Cards still require a verified certificate; use [overrides](customize.md#overrides) for their text instead.

## Previews

In normal use, the [`tailnet-preview` skill](../skills/tailnet-preview/README.md) handles previews for you: ask your coding agent *"Can I see this on my phone?"* or *"Publish three versions of the landing page"*, and it picks free HTTPS ports, publishes the app and its metadata card (`A`, `B`, `C`), verifies the link, and cleans up its mappings when you say you are done.

To display those previews at the top of `home/`:

- Set [`TAILHOME_PREVIEW_PORTS`](setup.md#preview-ports) on `tailhome` (for example `8443-8452`) so it scans the ports your agent uses. Port 443 is always checked and becomes a preview when its card sets `preview: true`.
- Previews appear only while their dev server answers, and disappear automatically when stopped. Use a [`temporary: false` override](customize.md#overrides) if you want an extra-port app to stay under **Apps** even when offline.
- Viewers need Tailscale access to the publishing device and HTTPS port; keep the host awake and the app running.

**Privacy and cookies:** Everyone who can open `tailhome` sees every listed preview's title, description, address and status, even without access to the app itself (keep secrets and internal paths out of titles and descriptions). Browsers also share cookies (including `HttpOnly` cookies) across all ports on a hostname; only publish trusted code on machines where you stay signed in to other apps.

### Preview cards

The [`tailnet-preview` skill](../skills/tailnet-preview/README.md) generates, serves and removes `/.well-known/tailhome.json` cards automatically. You only need the manual commands below if you are publishing a preview yourself from the terminal or writing a custom script.

<details>
<summary>Manual publishing and <code>/.well-known/tailhome.json</code> schema</summary>

Publish the app at `/`, then its card at `/.well-known/tailhome.json` on the same free HTTPS port:

```sh
tailscale serve --bg --https=8443 --set-path=/ http://127.0.0.1:3000
tailscale serve --bg --https=8443 --set-path=/.well-known/tailhome.json 'text:{"title":"Landing redesign","description":"New pricing layout","preview":true,"set":"Landing","version":"A"}'
```

Serve a JSON object with HTTP 200, at most 4096 bytes. All fields are optional:

| Field | Value |
|---|---|
| `title` | Plain text, up to 60 characters |
| `description` | Plain text, up to 140 characters |
| `icon` | dashboard-icons slug, up to 64 lowercase letters, digits or hyphens |
| `preview` | `true` marks a port-443 app as a preview |
| `set` | Group title, up to 60 characters; grouped within one host |
| `version` | One or two ASCII letters or digits, shown as a badge |

Each version needs its own app instance, port, card and URL. Before removing a manual publication, check that both mounts still belong to you:

```sh
tailscale serve --https=8443 --set-path=/.well-known/tailhome.json off
tailscale serve --https=8443 --set-path=/ off
```

Do not use whole-port `off` or `serve reset` on shared configuration.

</details>

### Card refresh and checks

When `TAILHOME_URL` is set in the agent's environment, the skill refreshes `home/` and confirms the listing automatically. Otherwise, click **refresh** on `home/` after publishing or updating a card.

## Using the page

- Select **refresh** to update the list. A discovery error leaves the last list visible.
- Select an Other row to copy connection details; customize these with a `copy` override.
- Use the **Appearance** menu to choose a mode and a palette, saved per browser.
- With [public sharing](setup.md#public-sharing) on, admins use a tile's globe to share the app or stop a share. A filled globe means the app is public. Shares keep running and stay in the summary if their tile is hidden later. Previews cannot be shared.
- On phones, use Safari's Add to Home Screen or Chrome's Install app.
- Open `home/go/jellyfin` by name, or `/go/devbox:8443` for a specific device port.

Status reflects tailhome's access, not yours:

| Indicator | Meaning |
|---|---|
| Green dot | App answered HTTP below 500, or accepted a TCP connection |
| Red dot | Check failed or device offline; reason shown below the name |
| Grey dot | Nothing to check |
| Green ring | HTTPS connected; app health not checked |
| Red ring | Extra-port app unreachable |

## Publish container apps

- [DockTail](https://github.com/marvinvr/docktail) publishes Tailscale Services. Its labels can set [tile descriptions and groups](integrations.md#docktail).
- [TSDProxy v2](https://github.com/almeidapaulopt/tsdproxy), [tsbridge](https://github.com/jtdowney/tsbridge) and [ScaleTail](https://github.com/tailscale-dev/ScaleTail) give each app its own device.
- Use `group` overrides to separate containers sharing tags, and `url` overrides for paths other than `/`.
- For stopped DockTail containers, set `DELETE_UNUSED_SERVICES=true` in DockTail or hide their tiles with `hidden: true`.
