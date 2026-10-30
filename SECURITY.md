# Security

Report vulnerabilities privately through [GitHub security advisories](https://github.com/mickey/tailhome/security/advisories/new). Include the tailhome version, how you deployed it, steps to reproduce and the impact.

Keep exploit details and secrets out of public issues.

## Security scope

tailhome has no login of its own. Your tailnet's access policy controls who can reach the `home` node, or `svc:home` with an OAuth client. See [Security and privacy](README.md#security-and-privacy) for catalog visibility.

Hiding a Service or device removes it from the page and JSON lists, but does not restrict access.

Keep OAuth credentials, auth keys and persistent state private, and redact them from reports.
