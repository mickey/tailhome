# Customize tiles

[Back to the README](../README.md).

## Overrides

Create `config/overrides.yaml` next to [`compose.yaml`](../compose.yaml). All fields are optional; [`overrides.example.yaml`](../overrides.example.yaml) lists them:

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
  url: vnc://nas.example.ts.net:5900   # Services: TCP-checked; devices: link only, HTTPS root still checked
whoami:
  web: true                 # always a tile, even before it answers HTTP
pgadmin:
  web: false                # under "Other" instead of a tile
gitea:
  copy: git@gitea.example.ts.net   # what a click copies; on a web Service, adds a row under "Other"
postgres:
  hidden: true
paperless-ngx:
  label: Paperless
```

Make the file readable (`chmod 644`) and its directories traversable by tailhome (`chmod 755 config` for the setup above).

Set `TAILHOME_OVERRIDES` to use another path. Without Docker, the default is `overrides.yaml` in the working directory. Edits take effect on refresh without a restart.

Keys are case-sensitive Service or device names; the `svc:` prefix is optional, so `jellyfin` and `svc:jellyfin` name the same Service and cannot both appear. Check logs for misspelled names or invalid fields if a refresh fails. `web` and `copy` apply only to Services; see [how Services become tiles](apps.md#services).

## URL schemes

Tile links and `/go/` accept `http`, `https`, `mailto`, `vnc`, `ssh`, `rdp`, `smb`, `ftp`, `sftp` and `telnet` URLs. Other schemes do not open.

## Homepage and Glance

These widgets show discovered apps and their status; they do not maintain existing service cards. The dashboard server must be on the tailnet and allowed to reach tailhome. Replace `home.<tailnet>.ts.net` with the address in tailhome's logs.

## Homepage

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

## Glance

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

`/api/services.json` returns the same grouped data as the page, notices included. `/api/list.json` returns every entry as one flat list in page order, each web Service once.

Entries carry `source: "service"` or `source: "device"`; Other entries omit `url` and include a `copy` value.
