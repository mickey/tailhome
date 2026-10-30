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

## Homepage and Glance

Your dashboard server must be on the tailnet and allowed to reach tailhome.
Replace `home.<tailnet>.ts.net` with the address in tailhome's logs.

### Homepage

Add this widget to Homepage's `services.yaml`:

```yaml
- Tailnet:
    - Tailnet Apps:
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

Homepage cannot filter items and shows Other rows without links. Glance shows their `copy` values.

### Glance

Add this widget to Glance's `glance.yml`:

```yaml
- type: custom-api
  title: Tailnet Apps
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

## JSON format

| Endpoint | Response |
|---|---|
| `GET /api/list.json` | Flat list in [page order](#page-order), suitable for widgets. |
| `GET /api/services.json` | Previews, app groups and Other, plus refresh time and errors. |

Entries include `name`, `label`, `description`, `icon`, `url`, `copy` and `status`. Other entries provide `copy` instead of `url`.
