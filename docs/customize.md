# Customize tiles

[Back to the README](../README.md).

## Overrides

Create `config/overrides.yaml` next to [`compose.yaml`](../compose.yaml). All fields are optional; [`overrides.example.yaml`](../overrides.example.yaml) lists them:

In this example, `devbox` is the publishing machine's host name; use your actual host name.

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
  url: vnc://nas.example.ts.net:5900   # Services: TCP-checked; devices: link only, origin checks unchanged
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
"devbox:8443":
  label: Design sandbox
  temporary: false          # device endpoint only; keep visible when down, still TLS-checked
```

Make the file readable (`chmod 644`) and its directories traversable by tailhome (`chmod 755 config` for the setup above).

Set `TAILHOME_OVERRIDES` to use another path. Without Docker, the default is `overrides.yaml` in the working directory. Edits take effect on refresh without a restart.

Keys are case-sensitive Service or device names; the `svc:` prefix is optional, so `jellyfin` and `svc:jellyfin` name the same Service and cannot both appear. Check logs for misspelled names or invalid fields if a refresh fails. `web` and `copy` apply only to Services; see [how Services become tiles](apps.md#services).

For devices, `devbox` means port 443 and `"devbox:8443"` means only port 8443. The `label`, `description`, `icon`, `url`, `group`, `order` and `temporary` settings under `devbox` affect only 443. When `devbox` names a device, `devbox: {hidden: true}` hides all its ports and cannot be undone by a port-specific override. If a Service has the same name, that key belongs to the Service and hiding it does not hide the device's extra ports. You can also hide one port.

`temporary: true` puts a device port in Previews. `temporary: false` makes it an app that stays visible when down once found. Using this setting on a known Service is an error. Extra ports default to previews, even with `preview: false` in a card; port 443 defaults to an app unless its card has `preview: true`. See the [reversible permanent-app recovery recipe](troubleshooting.md#a-permanent-device-app-moved-into-previews), [checks](apps.md#card-refresh-and-checks) and [preview visibility](apps.md#previews).

Nonempty `label`, `description` and `icon` overrides take priority over card fields, then device defaults. Empty strings use those fallbacks. A `url` override changes the link, not the device address checked or card read. Only cards can supply `set` and `version`, and only previews display them. See [certificate warnings](apps.md#devices-with-their-own-certificate).

### Page order

Previews come first: single previews, then sets grouped by host. Singles sort by `order`, label and device name with port. Sets sort by exact title, then host, then internal node ID. Within a set, version badges sort first in case-sensitive ASCII order, then `order`, label and device name with port. Missing badges come last. `order` cannot move or split a set.

App groups follow alphabetically, with entries sorted by `order`, label and name. Other comes last. A group named `Previews` shows as `Previews (apps)` while previews exist; its JSON group stays `Previews`. Previews keep their configured `group` in JSON.

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

`GET /api/list.json` returns a flat array in [page order](#page-order), or `[]` when empty. Each app or preview appears once; a web Service's copy row is not repeated. Set headings are not entries.

`GET /api/services.json` returns `previews`, `groups`, `other`, `updated`, and optional `error` and `notices`. `previews` is a sorted flat array, including `[]` when empty. Build set headings from `host` plus `set`. `groups` contains only apps. Discovery errors keep the last entries and `updated`, and add an error instead of publishing a partial list.

Entries have `name`, `source`, `label`, `description`, `group`, `icon`, `host`, `ports`, `url`, `copy`, `status` and `reason`. Optional empty fields are omitted. `source` is `"service"` or `"device"`. Device names are `devbox` for 443 and `devbox:8443` for an extra port. Other entries omit `url` and include `copy`.

Version 0.7 adds these fields to both JSON responses:

| Field | Meaning |
|---|---|
| `temporary` | `true` for previews; omitted means false. See [when previews are listed](apps.md#previews). |
| `set`, `version` | Optional validated card strings, only on previews. Match sets by `host` and title together. |
| `check` | `"tls"` for a TLS handshake or `"http"` for HTTP at `/`. Omitted on Services, which keep their HTTP/TCP checks. |
| `unverified` | Device certificate warning; omitted means false. Display text cannot clear it. `description` no longer contains "Unverified HTTPS". |

`status: "up"` with `check: "tls"` means only that the TLS listener answered, not that HTTP or the app works. `check` describes the status check, not card verification. A missing entry does not prove deletion, expiry or lack of access. No fields describe app processes, expiry, saved cards or schema version.

Both responses follow the [same visibility rules as the page](apps.md#previews). Version 0.7 changes ordering and which previews are listed as well as adding fields. Update clients that assume the old list.
