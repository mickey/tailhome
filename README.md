<div align="center">

# tailhome

**The start page that builds itself from your [Tailscale Services](https://tailscale.com/docs/features/tailscale-services).**<br>
Open `http://home/` to browse your Tailscale Services, with icons and live health checks.

[![Release](https://img.shields.io/github/v/release/mickey/tailhome)](https://github.com/mickey/tailhome/releases)
[![CI](https://github.com/mickey/tailhome/actions/workflows/ci.yml/badge.svg)](https://github.com/mickey/tailhome/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/github/license/mickey/tailhome)](LICENSE)

[Quick start](#quick-start) · [How it works](#how-it-works) · [Customize tiles](#customize-tiles) · [Containers](#containers) · [Troubleshooting](#troubleshooting)

</div>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshot-dark.png">
  <img alt="tailhome in a browser at home/, showing Services grouped into Home Lab, Media, Monitoring and Other" src="docs/screenshot.png">
</picture>

- **Automatic Service discovery.** tailhome lists reachable Services without API credentials and loads icons from [dashboard-icons](https://github.com/homarr-labs/dashboard-icons). New Services appear on the next refresh.
- **One small binary, no host Tailscale needed.** tailhome joins your tailnet through [tsnet](https://tailscale.com/docs/features/tsnet). The image is about 12 MB.
- **Health checks inside the tailnet.** A green dot means tailhome can reach the Service.

tailhome lists [Tailscale Services](https://tailscale.com/docs/features/tailscale-services), not machines. No Services yet? Define one in the [admin console](https://console.tailscale.com/admin/services), or publish containers with [DockTail](#containers).

tailhome is a **launcher, not a dashboard**: no widgets, no feeds. For those, use [Homepage](https://gethomepage.dev) or [Glance](https://github.com/glanceapp/glance).

---

## Quick start

> **Prerequisites:** at least one Tailscale Service, Tailscale 1.86 or later on your browsing devices, and **MagicDNS** plus **HTTPS Certificates** enabled in the [DNS settings](https://console.tailscale.com/admin/dns).

Create [`compose.yaml`](compose.yaml), then start tailhome:

```yaml
services:
  tailhome:
    image: ghcr.io/mickey/tailhome:latest
    env_file: [{ path: .env, required: false }] # optional, see "Groups and descriptions"
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
docker compose up -d
docker compose logs -f
```

Without a compose file: `docker run -d --name tailhome --restart unless-stopped -v tailhome-data:/data ghcr.io/mickey/tailhome:latest`, then `docker logs -f tailhome`.

Open the login link in the logs to add tailhome to your tailnet. When the logs show `serving https://home.<tailnet>.ts.net`, open `http://home/` from a tailnet device. Keep the trailing slash so your browser doesn't search for "home".

tailhome joins under your account as a device named `home`:

- **Disable key expiry** on the [Machines](https://console.tailscale.com/admin/machines) page. Untagged nodes expire after 180 days by default; the page shows the date until you disable it.
- **Check the address in the logs.** If a device or Service already uses `home`, Tailscale assigns another name, such as `home-1`.

To skip browser login, set `TS_AUTHKEY` to an [auth key](https://console.tailscale.com/admin/settings/keys). A tagged key also avoids key expiry, but its tag needs a `tagOwners` entry.

<details>
<summary><strong>Without Docker: binary, Go or systemd</strong></summary>

Download a binary from [Releases](https://github.com/mickey/tailhome/releases), or install it with Go:

```sh
go install github.com/mickey/tailhome@latest
tailhome
```

For systemd, run as root (add `/etc/tailhome/env` only with an OAuth client):

```sh
install -m 755 tailhome /usr/local/bin/
install -d /etc/tailhome
install -m 644 contrib/tailhome.service /etc/systemd/system/
systemctl enable --now tailhome
```

The unit stores state in `/var/lib/tailhome` and reads `/etc/tailhome/overrides.yaml`. Use `journalctl -u tailhome` to find the login link.

</details>

> [!NOTE]
> Keep the state directory (`/data` in Docker). Deleting it makes tailhome join as a new node; the old node remains in the admin console.

---

## How it works

- **Tiles.** Tiles open in your browser. A Service becomes a tile if it advertises port 443 or 80, has a `url` or `web: true` override, or answers HTTP on its first TCP port. Without a usable port (such as `do-not-validate`), tailhome tries HTTPS on 443, then HTTP on 80. A Service that needs that detection waits under "Other" until it first answers, even if it is down when tailhome starts.
- **Other.** Everything else goes under "Other", and a click copies what you need to connect: `ssh host` for port 22, `vnc://host` for 5900, a `postgres://`, `mysql://`, `redis://` or `mongodb://` URL for their default ports, else the hostname. Set `copy` to choose the value; on a web Service, `copy` also adds a row under "Other". Use `web: false` to keep a Service under "Other".
- **Health.**
  - **Green:** an HTTP response below 500, including redirects; a successful TCP connection for "Other" and non-HTTP `url` overrides (`ssh://`, `vnc://`).
  - **Red:** the check failed. Hover over the dot for the reason. A tile that goes down stays in its group.
  - **Grey:** nothing to check, such as a Service with no TCP port.
- **Refresh.** tailhome reloads and checks Services every 5 minutes. Select **refresh** to check now, at most once every 10 seconds. If discovery or overrides fail, the page keeps the last list and shows the error.
- **Notices.** Without credentials, the page warns when the node's key will expire or when the node got another name, such as `home-1`. It rechecks both on every refresh.
- **Home screen.** On a phone, use **Add to Home Screen** (Safari) or **Install app** (Chrome) to open tailhome like an app. Coming back to the page after 5 minutes, in the app or a tab, reloads it.
- **JSON.** `/api/services.json` returns the same data as the page, notices included. `/api/list.json` returns every Service as one flat list, in page order.

---

## Customize tiles

Default tile fields:

| Field | Source |
|---|---|
| Label | The Service's display name, or its name in title case |
| Description | The Service's description, with an OAuth client |
| Group | The Service's first tag, with an OAuth client: `tag:home-lab` becomes "Home Lab". Other Services use "Services". |
| Icon | The [dashboard-icons](https://github.com/homarr-labs/dashboard-icons) entry named after the Service, or the label's first letter |

To change them, create `config/overrides.yaml` next to `compose.yaml`. All fields are optional; [`overrides.example.yaml`](overrides.example.yaml) lists them:

```yaml
jellyfin:
  group: Media
  order: -10                # lower sorts first; unset counts as 0, ties sort by label
grafana:
  label: Grafana
  description: Metrics and dashboards
  icon: grafana             # a dashboard-icons name, or a full image URL
  url: https://grafana.example.ts.net/d/main
nas:
  url: vnc://nas.example.ts.net:5900   # also ssh, rdp, smb, ftp, sftp, telnet; TCP-checked
whoami:
  web: true                 # always a tile, even before it answers HTTP
pgadmin:
  web: false                # under "Other" instead of a tile
gitea:
  copy: git@gitea.example.ts.net   # what a click copies; on a web Service, adds a row under "Other"
postgres:
  hidden: true
```

Make the file readable by everyone (`chmod 644`). Without Docker, tailhome reads `overrides.yaml` from its working directory; set `TAILHOME_OVERRIDES` to use another path.

tailhome rereads overrides on every refresh, so edits need no restart. Keys are Service names, with or without `svc:`. Unknown fields fail the refresh: correct them and refresh again. tailhome ignores unmatched names and logs a warning; check their spelling.

---

## Containers

[DockTail](https://github.com/marvinvr/docktail) and [TSDProxy](https://github.com/almeidapaulopt/tsdproxy) v3 (`services: true`) publish Docker containers as Services, so their tiles appear on their own. TSDProxy v2, [tsbridge](https://github.com/jtdowney/tsbridge), and [ScaleTail](https://github.com/tailscale-dev/ScaleTail) create devices instead; tailhome doesn't list them.

<details>
<summary><strong>Known quirks: groups, labels, paths, stopped containers</strong></summary>

- **Groups.** With an OAuth client, DockTail's default `tag:container` and TSDProxy's shared tags put tiles in one group. Set `docktail.tags` per container (each tag needs `tagOwners` and `autoApprovers` entries), or use `group` overrides.
- **Labels.** These tools omit display names and may clear names you set in the admin console. Use `label` overrides.
- **Descriptions.** With an OAuth client, `docktail.service.description` appears on the tile.
- **Paths.** Tiles open `/` because the API omits `docktail.service.path`. Set `url` overrides.
- **Stopped containers.** DockTail keeps their Services, leaving red tiles. Set `DELETE_UNUSED_SERVICES=true` in DockTail or `hidden: true` in overrides. TSDProxy deletes stopped containers' Services.

</details>

---

## Homepage and Glance

Already using one of them? Keep it: Homepage and Glance list what you declare in YAML or Docker labels, and tailhome can feed them every Tailscale Service it discovers, with its health.

<details>
<summary><strong>Show the Homepage and Glance widgets</strong></summary>

Both tools fetch the list server-side, not from your browser, so the machine running them must be on your tailnet and your policy must let it reach tailhome. Replace `home.<tailnet>.ts.net` with the address tailhome logs.

**Homepage**, in `services.yaml`:

```yaml
- Tailnet:
    - Tailscale Services:
        icon: tailscale.svg
        href: https://home.<tailnet>.ts.net/
        widget:
          type: customapi
          url: https://home.<tailnet>.ts.net/api/list.json
          display: dynamic-list
          mappings:
            name: label
            label: status
            target: "{url}"
```

**Glance**, in `glance.yml`:

```yaml
- type: custom-api
  title: Tailscale Services
  title-url: https://home.<tailnet>.ts.net/
  url: https://home.<tailnet>.ts.net/api/list.json
  cache: 1m
  template: |
    <ul class="list list-gap-10 collapsible-container" data-collapse-after="10">
    {{ range .JSON.Array "" }}
      <li class="flex items-center gap-10">
        <img src="{{ .String "icon" }}" width="20" height="20" alt="" onerror="this.style.visibility='hidden'">
        {{ if .String "url" }}<a class="color-highlight" href="{{ .String "url" }}">{{ .String "label" }}</a>{{ else }}<span>{{ .String "label" }}</span> <span class="color-subdue">{{ .String "copy" }}</span>{{ end }}
        <span class="{{ if eq (.String "status") "up" }}color-positive{{ else if eq (.String "status") "down" }}color-negative{{ else }}color-subdue{{ end }}" title="{{ .String "status" }}">●</span>
      </li>
    {{ end }}
    </ul>
```

</details>

---

## Groups and descriptions

| | Without credentials (default) | With an OAuth client |
|---|---|---|
| Setup | Login link or auth key | ACL entries, a Service definition and an OAuth client; needs admin access |
| Services listed | Those tailhome can reach | All Services in the tailnet |
| Groups | One, unless you set `group` overrides | From each Service's first tag |
| Descriptions | From overrides only | From the admin console |
| Page address | The node's name, `home` | The Service `svc:home` |

<details>
<summary><strong>Set up an OAuth client</strong></summary>

1. **Allow `tag:tailhome`** in your [access controls](https://console.tailscale.com/admin/acls):

   ```jsonc
   "tagOwners": {
     "tag:tailhome": ["autogroup:admin"]
   },
   "autoApprovers": {
     "services": { "svc:home": ["tag:tailhome"] }
   }
   ```

   Tailscale's default policy allows everyone to reach everything. With a stricter policy, also add:

   ```jsonc
   "grants": [
     // Who can open the start page
     { "src": ["autogroup:member"], "dst": ["svc:home"], "ip": ["*"] },
     // What tailhome may health-check
     { "src": ["tag:tailhome"], "dst": ["*"], "ip": ["*"] }
   ]
   ```

2. **Define the `home` Service:** on the [Services](https://console.tailscale.com/admin/services) page, select **Advertise → Define a Service**, name it `home`, enter ports `443,80`, leave tags empty, and select **Define Service**. First rename or remove any device named `home`, including a tailhome node that joined without credentials.

3. **Create an OAuth client:** in [Trust credentials](https://console.tailscale.com/admin/settings/trust-credentials), add a credential with custom scopes **General → Services → Read** to list Services and **Keys → Auth Keys → Write** with `tag:tailhome` to join the tailnet. Copy the client ID and secret; Tailscale shows the secret only once.

4. **Save the credentials in `.env`** next to `compose.yaml` (see [`.env.example`](.env.example)), then restart. If tailhome already joined without credentials, reset its state first so it joins again with its tag: `docker compose down -v && docker compose up -d`. Overrides in `./config` are kept.

   ```sh
   TAILHOME_CLIENT_ID='your-client-id'
   TAILHOME_CLIENT_SECRET='tskey-client-your-secret'
   ```

Without Docker, set the same two variables before starting tailhome.

</details>

---

## Configuration

| Variable | Default | Description |
|---|---|---|
| `TAILHOME_CLIENT_ID` | unset | OAuth client ID. Set both or neither. |
| `TAILHOME_CLIENT_SECRET` | unset | OAuth client secret |
| `TAILHOME_STATE_DIR` | `/data` in Docker, else `tsnet-tailhome` in your user config dir | Where tailhome keeps its tailnet identity |
| `TAILHOME_OVERRIDES` | `/config/overrides.yaml` with the compose file above, `/data/overrides.yaml` in the bare image, else `overrides.yaml` | Path to the overrides file |
| `TAILHOME_REFRESH` | `5m` | How often to reload and recheck Services |
| `TAILHOME_SERVICE` | `svc:home` | Service that serves the page (OAuth only) |
| `TAILHOME_TAG` | `tag:tailhome` | Node tag (OAuth only) |
| `TAILHOME_HOSTNAME` | `home`, or `tailhome` with OAuth | Node name in the tailnet |
| `TAILHOME_TAILNET` | the OAuth client's tailnet | Tailnet to list (OAuth only) |
| `TS_AUTHKEY` | unset | Auth key instead of browser login or an OAuth-minted key. With OAuth, it must allow `TAILHOME_TAG` (default `tag:tailhome`). |
| `TS_NO_LOGS_NO_SUPPORT` | unset | Set to `true` to stop uploading tsnet diagnostic logs to Tailscale |

---

## Troubleshooting

| Symptom | Cause and next step |
|---|---|
| The page lists no Services | It shows what to check: tailhome lists Services, not machines, and the node must be allowed to reach them. If the tailnet doesn't yet share Services with nodes, use an OAuth client. |
| `home/` doesn't load | Without OAuth, check the logged address for a name such as `home-1`. With OAuth, define `svc:home` (step 2), add `autoApprovers`, or approve its host on the Services page. Logs warn if the Service is missing. |
| `home/` doesn't resolve on one device | Enable MagicDNS and use Tailscale 1.86 or later. On Linux before 1.94, run `sudo tailscale set --accept-routes`. |
| `https://home/` won't connect | HTTPS requires the full name. Open `http://home/` to redirect there. |
| `https://home.<tailnet>.ts.net/` won't connect | Enable HTTPS Certificates in the DNS settings. |
| The first HTTPS load is slow or times out | Tailscale may still be issuing the certificate. Retry in a minute. |
| tailhome exits with `this node joined without a tag` | It joined without credentials earlier. Reset its state (step 4) so it joins again with its tag. |
| tailhome exits with `joining the tailnet: creating an auth key for tag:tailhome: …` | Give the OAuth client Auth Keys: Write for `tag:tailhome` and add the tag to `tagOwners`. |
| "Refresh failed: …" mentions `oauth2` or `403` | Give the OAuth client Services: Read. |
| A dot is red but the Service opens | Check whether policy lets tailhome reach the Service. With OAuth, add the grants from step 1. |
| An override has no effect | Check the logs for a name warning, and that `TAILHOME_OVERRIDES` points at the file: a missing file counts as no overrides. |
| "Refresh failed: … permission denied" | Make the overrides file readable (`chmod 644`). |

---

## Security and privacy

- **Service names.** Anyone who can open tailhome sees all listed Service names on the page and in `/api/services.json` and `/api/list.json`, even Services they can't open. Restrict access to tailhome if needed.
- **Icons.** Browsers request icons from `cdn.jsdelivr.net`, revealing Service names to jsDelivr. Use `icon` overrides with your own URLs to avoid this.
- **Logs.** tailhome uploads diagnostic logs to Tailscale, like other Tailscale nodes. Set `TS_NO_LOGS_NO_SUPPORT=true` to stop uploads.

Report vulnerabilities as described in [SECURITY.md](SECURITY.md). To contribute, see [CONTRIBUTING.md](CONTRIBUTING.md). tailhome is released under the [MIT license](LICENSE).
