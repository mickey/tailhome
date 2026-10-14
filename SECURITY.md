# Security

Report vulnerabilities privately through [GitHub security advisories](https://github.com/mickey/tailhome/security/advisories/new).
Include the tailhome version, deployment method, reproduction steps, and impact.
Do not publish exploit details or secrets in a public issue.

## Security scope

The tailhome page is visible to anyone who can reach `svc:home` under your tailnet's access policy. It has no separate login or per-viewer filtering. Service names, descriptions, and links shown on the page are therefore shared with those viewers.

Restrict access with Tailscale policies. Hiding a Service in tailhome only removes it from the page; it does not restrict access to that Service. Keep OAuth credentials, auth keys, and persistent state private, and redact them from bug reports.
