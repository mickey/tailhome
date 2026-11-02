# Customize tiles

[Back to the README](../README.md).

## Overrides

With Compose, create `config/overrides.yaml` next to [`compose.yaml`](../compose.yaml). Use Service names or the Tailscale device name from its preview URL.

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

- Fields are optional; see [`overrides.example.yaml`](../overrides.example.yaml) for examples.
- Names are case-sensitive. `"devbox:8443"` targets that port; `devbox` targets port 443.
- `hidden: true` on a device name hides all its ports.
- `web` and `copy` apply only to Services; `temporary` applies only to device apps.
- Use `temporary: true` for Previews or `false` for Apps.

For `docker run`, create the same file, run `docker stop tailhome` and `docker rm tailhome`, then rerun your install command with these arguments before the image name. Keep the `tailhome-data` volume.

```sh
-v "$PWD/config:/config:ro" \
-e TAILHOME_OVERRIDES=/config/overrides.yaml
```

For another location, set [`TAILHOME_OVERRIDES`](setup.md#environment-variables).

### Page order

Previews come first, app groups follow alphabetically, and Other comes last.


## URL schemes

Tile links and `/go/` accept `http`, `https`, `mailto`, `vnc`, `ssh`, `rdp`, `smb`, `ftp`, `sftp` and `telnet` URLs. Other schemes do not open.

## JSON format

For ready-made Homepage and Glance widgets, see [integrations](integrations.md).

| Endpoint | Response |
|---|---|
| `GET /api/list.json` | Flat list in [page order](#page-order), suitable for widgets. |
| `GET /api/services.json` | Previews, app groups and Other, plus refresh time and errors. |

Entries include `name`, `label`, `description`, `icon`, `url`, `copy` and `status`. Icons drawn in two colors also have `iconDark`, the variant for dark backgrounds. Other entries provide `copy` instead of `url`.
