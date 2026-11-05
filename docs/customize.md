# Customize tiles

[Back to the README](../README.md).

## Overrides

With Docker Compose, create `config/overrides.yaml` next to [`compose.yaml`](../compose.yaml). Keys are Service names (with or without `svc:`) or the Tailscale device name from a device or preview URL (such as `devbox` or `"devbox:8443"`).

```yaml
jellyfin:
  group: Media
  order: -10                # lower sorts first
grafana:
  label: Grafana
  description: Metrics and dashboards
  icon: grafana             # a dashboard-icons name, or a full image URL
  url: https://grafana.example.ts.net/d/main
whoami:
  web: true                 # show as a tile
pgadmin:
  web: false                # under "Other" instead of a tile
gitea:
  copy: git@gitea.example.ts.net
postgres:
  hidden: true
paperless-ngx:
  label: Paperless
"devbox:8443":
  label: Design sandbox
  temporary: false          # keep this device app under Apps, even offline
```

Run `chmod 644 config/overrides.yaml` and `chmod 755 config`, then refresh the page to apply changes.

| Field | Applies to | Effect |
|---|---|---|
| `label` | Services, devices | Tile title shown on the page |
| `description` | Services, devices | Subtitle shown below the label |
| `icon` | Services, devices | [dashboard-icons](https://github.com/homarr-labs/dashboard-icons) slug (such as `grafana`) or a full image URL |
| `group` | Services, devices | Section heading (defaults to `Apps`, or the first tag with OAuth) |
| `order` | Services, devices | Sort weight within a group; lower numbers sort first (default `0`, ties sort by label) |
| `hidden` | Services, devices | `true` hides the entry from the page and JSON endpoints; on a bare device name, hides all its ports |
| `url` | Services, devices | Target URL when clicked (must use an [allowed URL scheme](#shortcuts-and-url-schemes)) |
| `web` | Services only | `true` forces a tile even before HTTP responds; `false` moves the Service to **Other** |
| `copy` | Services only | Value copied when clicking the **Other** row; on a web Service, also adds a row under **Other** |
| `target` | Services, devices | Name of the browser tab the tile opens in; tiles with the same name share one tab. Overrides `TAILHOME_LINK_TARGET`. The tab is not opened with `noopener`, because browsers would otherwise open a new tab each time, so only name tabs for apps you trust. Names starting with `_` are browser keywords (`_blank` opens a new tab each time) |
| `temporary` | Devices only | `true` lists the endpoint under **Previews** (hidden when offline); `false` keeps an extra-port app under **Apps** even when offline |

All fields are optional and keys are case-sensitive (`"devbox:8443"` targets port 8443; `devbox` targets port 443). See [`overrides.example.yaml`](../overrides.example.yaml) for a complete template.

For `docker run`, create the same file, run `docker stop tailhome` and `docker rm tailhome`, then rerun your install command with these arguments before the image name. Keep the `tailhome-data` volume.

```sh
-v "$PWD/config:/config:ro" \
-e TAILHOME_OVERRIDES=/config/overrides.yaml
```

For another location, set [`TAILHOME_OVERRIDES`](setup.md#environment-variables).

### Page order

Previews come first, app groups follow alphabetically, and Other comes last.


## Shortcuts and URL schemes

Open `http://home/go/<name>` (case-insensitive) to jump straight to any listed tile by its Service or device name, such as `home/go/jellyfin` or `home/go/devbox:8443`.

Tile links and `/go/` redirects accept `http`, `https`, `mailto`, `vnc`, `ssh`, `rdp`, `smb`, `ftp`, `sftp` and `telnet` URLs. Other schemes are not linked.

## JSON format

For ready-made Homepage and Glance widgets, see [integrations](integrations.md).

| Endpoint | Response |
|---|---|
| `GET /api/list.json` | Flat list in [page order](#page-order), suitable for widgets. |
| `GET /api/services.json` | Previews, app groups and Other, plus refresh time and errors. |

Entries include `name`, `label`, `description`, `icon`, `url`, `copy` and `status`. Icons drawn in two colors also have `iconDark`, the variant for dark backgrounds. Other entries provide `copy` instead of `url`.
