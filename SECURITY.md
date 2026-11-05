# Security

Report vulnerabilities privately through [GitHub security advisories](https://github.com/mickey/tailhome/security/advisories/new). Include the tailhome version, how you deployed it, steps to reproduce and the impact.

Keep exploit details and secrets out of public issues.

## Security scope

tailhome has no login of its own. Your tailnet's access policy controls who can reach the `home` node, or `svc:home` with an OAuth client. See [Security and privacy](README.md#security-and-privacy) for catalog visibility.

Hiding a Service or device removes it from the page and JSON lists, but does not restrict access.

Keep OAuth credentials, auth keys and persistent state private, and redact them from reports.

## Public sharing

Setting `TAILHOME_SHARE_ADMINS` enables sharing for those exact Tailscale login names, with no separate sign-in. See [setup](docs/setup.md#public-sharing) for policy and lifetime controls.

- Anyone on the internet or tailnet can reach the shared app's routes, with public paths matching upstream paths.
- Both connections use the same proxy, Stop and expiry controls. Limits cap concurrent connections and requests; there is no rate limiting.
- Only WebSocket upgrades are allowed.
- Client-supplied identity and forwarding headers are stripped. Funnel visitors' public IP is sent as `X-Forwarded-For`; tailnet callers' IPs are never sent.
- Apps see connections from the share node. Never share apps that trust identity headers, skip login for tailnet peers or lock out users by source IP.
- Preview shares last at most 24 hours and check only the card endpoint every 10 seconds. Card changes or removal stop sharing at the next check; unreachable endpoints stop it after three failed checks, about 30 seconds.
- With a separate card mount (the skill's default) or no card, stopping only the dev server leaves the share up with 502 errors. A different app on the same port without republishing stays public until the share ends.
- Running preview shares do not follow catalog changes to tags, overrides or preview ports. They continue until expiry, Stop, removal or a card change.
- The OAuth secret can create pre-approved, Funnel-capable nodes with access to everything granted to `tag:tailhome-share`, outside tailhome's controls.
- Run without root or a Docker socket, and protect the secret, state directory and backups, including certificates.
- In Service mode, tailhome trusts `Tailscale-User-Login` on a loopback listener. Local users or processes in the same host or container network namespace can impersonate admins; keep the listener private and untrusted processes out.
- Sibling `.ts.net` sites can set parent-domain cookies and send requests with `SameSite` cookies. Open untrusted shares in a separate browser from tailnet administration.
- Hostnames and the tailnet name stay in public certificate logs. Random names are not secrets.
- Run only one tailhome process per state directory to avoid losing saved shares.

### If the OAuth secret or state leaks

1. Stop tailhome and revoke the OAuth client.
2. Remove the Funnel attribute for `tag:tailhome-share` from your policy.
3. Delete every `tag:tailhome-share` device in [Machines](https://console.tailscale.com/admin/machines), including any tailhome did not record.
4. Rotate app and node secrets, fix the cause, create a new OAuth client and re-enable sharing.
