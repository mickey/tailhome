# Security

Report vulnerabilities privately through [GitHub security advisories](https://github.com/mickey/tailhome/security/advisories/new).
Include the tailhome version, deployment method, reproduction steps, and impact.
Do not publish exploit details or secrets in a public issue.

## Security scope

tailhome has no login of its own: whoever can reach it under your tailnet's access policy (the `home` node, or `svc:home` with an OAuth client) sees every listed Service, as described in the README's [Security and privacy](README.md#security-and-privacy). Hiding a Service in tailhome only removes it from the page; it does not restrict access to that Service. Keep OAuth credentials, auth keys, and persistent state private, and redact them from reports.
