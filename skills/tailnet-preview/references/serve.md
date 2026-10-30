# Serve reference and installation

## Installation and updates

Install the whole reviewed `skills/tailnet-preview/` directory on the app's host, with access to its loopback server and connected Tailscale CLI. This independent tailhome skill is not an official Tailscale product.

Use Python 3.9+ on Linux or macOS; no package dependencies are required. Check `python3 --version` in the agent's environment.

Follow the client's documented skill directory and activation method, then reload or restart the client. Keep the helper beside SKILL.md and resolve its absolute path:

`python3 /absolute/skill/scripts/tailnet_preview.py ...`

Update the full directory from reviewed or pinned source. Keep one canonical copy with links to it and private ownership state outside it. Never distribute state with the skill or claim you validated an untested client.

<details>
<summary>Client paths and installer</summary>

Use `~/.agents/skills` for omp, Codex, OpenCode or Gemini CLI. Check other clients' documented paths. For Claude Code, symlink `~/.claude/skills/tailnet-preview` to the canonical directory. Review an existing installation or link's owner and target before replacing it.

For project installations, use `.agents/skills/` or `.claude/skills/`. With omp, you can also use `~/.omp/agent/skills/tailnet-preview/` or `.omp/skills/tailnet-preview/`.

To use the optional installer, run `npx skills add mickey/tailhome --skill tailnet-preview -g`. Select clients interactively or with `-a claude-code -a codex`. Only the installer requires Node.

Choose installer targets carefully:

- There is no `-a omp` target. Use `-a pi` or `-a codex` to install into the shared `~/.agents/skills`, which omp reads.
- `-a universal -g` writes `~/.config/agents/skills`, not `~/.agents/skills`.

Activate the skill with `/tailnet-preview` in Claude Code, `$tailnet-preview` or `/skills` in Codex, or `/skill:tailnet-preview` in omp when skill commands are enabled. For other clients, load SKILL.md and its bundled references through their documented mechanism.

</details>

<details>
<summary>Python and platform limits</summary>

Do not use native Windows. Do not assume Windows localhost and a WSL Tailscale daemon are interchangeable; WSL publication has not been established.

If Python is missing, ask the machine's owner to install a current Python 3 from python.org or their existing package manager, then restart the agent. If uv is already in use, you can also run `uv run scripts/tailnet_preview.py ...` from the skill directory.

For untrusted-certificate errors on python.org macOS builds, ask the machine's owner to run `Install Certificates.command`. Keep certificate verification enabled.

</details>

### Find the Tailscale CLI

The helper tries these CLI locations in order:

1. `TAILSCALE`, if supplied.
2. An executable on PATH.
3. `/Applications/Tailscale.app/Contents/MacOS/Tailscale`, the App Store app's bundled CLI.

If you set `TAILSCALE`, use one executable path or name, never a shell command, alias or argv fragment. An invalid value fails without fallback.

If needed, set `TAILSCALE=/Applications/Tailscale.app/Contents/MacOS/Tailscale` in the environment that launches the agent. Restart GUI-launched agents after environment changes. Use the installed variant. Do not install another daemon or switch variants for this skill.

Use a dry run to see the chosen executable. For macOS subprocesses, the helper sets `TAILSCALE_BE_CLI=1` without changing its parent environment. The standalone app's owner can install `/usr/local/bin/tailscale` through Settings > CLI integration.

See the official [CLI documentation](https://tailscale.com/docs/reference/tailscale-cli) and [macOS variants](https://tailscale.com/docs/concepts/macos-variants).

### Read preflight results

Run `preflight` before asking approval. Include permission warnings and blockers in one approval message. Add optional `--ports` to report free slots under publication's eligibility rules. Get approval before publishing even when ports are free.

<details>
<summary>Preflight checks and cookie scope</summary>

Preflight reads the CLI version, status, optional Linux operator preferences and Serve configuration. It creates no ownership state and makes no Tailscale changes.

`machine_ports` lists the ports this machine name already serves through Serve, including your previews. It excludes Services, which have their own names. Treat `null` as a failure to read Serve.

Account for cookie sharing before publishing:

- Browsers send this name's cookies to every port, including `HttpOnly` cookies.
- Preview servers receive cookies from current and past apps on this name.
- Cookies set for the whole tailnet domain reach Services too.
- Preflight cannot see apps serving ports themselves or browser-stored cookies.

Preflight exits nonzero only for a missing CLI, disconnected or logged-out backend, or unavailable HTTPS. Read warnings for other uncertain checks. HTTPS counts as available when top-level `CertDomains` in `tailscale status --json` lists certificate domains, which requires HTTPS certificates to be enabled. Do not rely on MagicDNS alone or treat this as a live certificate issuance test.

On Linux, root or the configured `OperatorUser` likely has Serve write permission; otherwise the helper reports `serve_write:"denied_likely"`. Ask the owner to run `sudo tailscale set --operator=$USER` once; never run it yourself. Give the same fix for access-denied Serve errors. On macOS with open source `tailscaled`, only accounts outside the `admin` group need it. The helper never elevates itself.

Treat `unknown` as unconfirmed write permission. The helper reports it when preferences are unreadable or the CLI comes from a macOS GUI variant.

</details>

## Host, approval and direct links

For direct links alone, omit `--home` and trusted `TAILHOME_URL`. Both are optional on publish, status and check. Do not search for or install tailhome.

A tailnet administrator must enable MagicDNS and HTTPS certificates. Policy must let reviewers reach the approved ports. Do not treat CLI connectivity as proof that Serve is ready.

If Serve returns a consent URL or permission error, preserve the actual stderr. Ask the user or administrator to finish approved setup, then retry. Do not authenticate, provision, elevate or change policy.

Ask reviewers to connect Tailscale before opening links and use an account approved for the publishing machine and each preview's HTTPS port. Keep the host awake, connected and running the app. Do not rely on a background Serve mapping to host or restart it. Do not claim host-side verification proves viewer connectivity or policy.

<details>
<summary>Discovery and outside-tailnet reviewers</summary>

The person running tailhome configures `TAILHOME_PREVIEW_PORTS` separately for extra-port discovery. Different hosts can reuse port numbers. For different pools, that person configures the launcher with all distinct extra ports, up to 32 total. Keep publication approval, access policy and launcher discovery separate.

A nonempty `TAILHOME_DEVICE_TAGS` filter excludes untagged laptops; an empty filter includes untagged devices. Do not suggest tagging a personal laptop for discovery. Tags replace its user identity and change applicable policy. See [preview setup](https://github.com/mickey/tailhome/blob/main/docs/setup.md#preview-ports).

For outside-tailnet reviewers, arrange an administrator-approved device share or invitation. This skill grants no access and provides no public fallback.

</details>

## App startup

Get startup permission to run the project's existing development command in a persistent session managed by your agent client. Keep it running after your response. Get permission beyond HTTPS-port approval for app startup, dependency installation, migrations and unrelated services.

Keep each version's upstream stable with separate worktrees/checkouts or supported per-instance configuration. Do not rely on branch names alone to create independent runtimes.

For a static HTML directory with no project startup command, get explicit permission to run a static server before using:

```sh
python3 -m http.server <authorized-loopback-port> --bind 127.0.0.1 --directory <absolute-copy-directory>
```

## Card schema

Supply exactly one non-null UTF-8 JSON object, at most 4096 bytes, with only trailing whitespace. Serve it at `/.well-known/tailhome.json` on each preview. Reserve that path and all paths beneath it for the card.

Write plain text and, optionally, a dashboard-icons slug. Never include HTML, credentials or source paths. Put the visible difference first in titles; describe differences and inspection tasks in descriptions. Keep workspace directories and internal review or iteration labels out of titles, descriptions and sets unless the user asked reviewers to see them.

| Field | Accepted value |
| --- | --- |
| `title` | Optional string, at most 60 code points |
| `description` | Optional string, at most 140 code points |
| `set` | Optional string, at most 60 code points |
| `icon` | Optional string, at most 64 ASCII characters matching `[a-z0-9-]+` |
| `version` | Optional string, one or two ASCII letters/digits |
| `preview` | Boolean, never null; the helper writes `true` |

For a comparison, use the same exact short user-facing `set` for every version, such as `Product pricing` or `Product heroes`. Give each a distinct badge, such as `A`, `B`, `C` or `1`, `2`, `3`, and name its approach in the title.

<details>
<summary>Validation and comparison matching</summary>

Reject duplicate keys, including unknown keys. Ignore other unknown fields.

Reject Unicode control characters and U+2028/U+2029 before trimming string fields, including leading or trailing tabs, newlines and those separators. Then trim ordinary Unicode whitespace and treat empty results as absent. Reject the whole card if a known field is invalid. Ignore Content-Type because Serve text mounts return `text/plain; charset=utf-8`.

Match comparisons within one host using exact trimmed text. Use badges to identify alternatives, not results. Keep run uniqueness in the non-secret local ID. Distinguish unrelated comparisons with meaningful user-facing set qualifiers.

Assume everyone who can open home can read every listed title, description, address and status, regardless of app access. Treat hiding a preview as a change for all viewers, not a way to revoke access.

A card does not configure discovery ports, grant access, create redirects, set an expiry or authorize cleanup.

</details>

## Verification and home confirmation

Choose `--route`, a successful 2xx `--expected-status` and identifying `--expect-text` for the intended screen. Put identifying text within the first 1 MiB of the app response; text beyond that probe limit counts as missing. Without `--expect-text`, use the status code alone to decide.

For handoff, put each title/badge, verified clickable HTTPS URL and inspection task on one line. Report results as described in [Confirm home listing](#confirm-home-listing).

<details>
<summary>Probe limits and transports</summary>

Choose probe text that appears without running JavaScript; probes cannot check text added only by JavaScript. Card probes read at most 4097 bytes to enforce the 4096-byte limit. Probes follow no redirects and use normal certificate verification.

For exact Self or Peer DNS names and service DNS names in `ExtraRecords`, the helper sends HTTP/1.1 through the chosen CLI's `tailscale nc`. TLS uses the URL FQDN for SNI and certificate hostname verification. This works with TUN and userspace networking without OS MagicDNS resolution. Other hosts use normal urllib transport.

The first HTTPS card check retries transport failures for up to 90 seconds while tailscaled obtains its certificate. Make sure Python trusts public certificate authorities.

</details>

### Report failures

Report verification failures from `error` and `warnings`, including each warning's original probe stage, route, transport, elapsed time and underlying error. Include certificate and transport errors. Do not replace diagnostics with "See warnings". Keep TLS verification and authority boundaries intact.

For failed publication, report `compensation_completed` and `ownership_record_remains`. Compensation removes only unchanged owned mounts. Mark the version incomplete and say whether recoverable state remains. Never report retained state as a successful preview.

Keep other successful previews and their app sessions running. Status never removes mounts. Compensation does not authorize app shutdown; get separate permission.

### Confirm home listing

Report `published`, `app_verified`, `card_verified` and `listed` separately. For `listed:null`, explain that no home was configured and hand off direct links. Run home-backed status checks sequentially.

Do not treat `listed:false` as proof that the app is inaccessible. If direct checks pass, say "Published; home listing not confirmed." Keep the independently verified direct preview usable.

<details>
<summary>Refresh and match rules</summary>

With a trusted home URL, the helper makes at most three refresh attempts, at least eleven seconds apart after the first, with eight-second request timeouts. If a refresh POST times out, the helper still checks the catalog. Keep launcher and publishing-host clocks within a few seconds of each other.

The helper requires a fresh `updated` timestamp and no catalog error in `/api/services.json`. In `/api/list.json`, it requires the exact URL, description, set and version, plus the label when the recorded card has a title. Absent optional description, set and version match empty strings. Do not accept a refresh response or 303 alone; the shared cooldown may skip card work.

</details>

## Local state and set selectors

Use unique IDs and distinct set names for unrelated comparisons on this OS account. List and clean only this account's recorded publications. If separate OS accounts share a Tailscale daemon, serialize publishing externally or use disjoint approved pools; the accounts have separate locks.

Supply approved pools as comma-separated decimal ports/ranges. Include at most 32 distinct ports; there is no built-in default. Get explicit approval for port 443. `8443-8452` provides ten host-wide slots before occupied or ineligible ports are excluded. Capacity is shared across sets; upstream ports are separate from Serve ports.

Plain `list` needs neither `--set` nor a remote catalog. To select a set, use `list --set NAME`. The helper validates and trims the selector, then matches the recorded card's exact case-sensitive set. Membership comes from local records, not the catalog.

Choose either `cleanup --id ID` or `cleanup --set NAME`; they are mutually exclusive. Report each selected ID's removal outcome and warnings, plus the aggregate selected count. Report an empty selection as an explicit no-op.

Never treat set labels or remote catalogs as ownership. Preserve other sets and foreign mounts. Leave apps, worktrees and packages intact during cleanup; they require separate permission.

<details>
<summary>State storage and recovery</summary>

Use an absolute `XDG_STATE_HOME` if you set it. Ownership state defaults to `~/.local/state/tailnet-preview/`, outside the installation, with directories mode 0700 and files mode 0600. Before changing Serve, the helper records its plan, then updates the record after observing the result. On failure, it removes only unchanged mounts it owns.

Records belong to this OS account, not a Tailscale user or team. Different machines can reuse IDs and sets. Neither tailhome nor another Serve tool shares this helper's cleanup registry or lock.

Set cleanup snapshots local selections under the lock, checks current node identity/FQDN and applies single-ID mount checks. Conflicts return nonzero while independent members continue. Use `--dry-run` to print per-ID commands without removal. The helper keeps changed members recorded for recovery.

</details>

<details>
<summary>List fields and app checks</summary>

Plain `list` and `list --set NAME` entries in `previews[]` keep all ownership-record fields, including raw recovery `card`, and add decoded `title`, `description`, `set` and `version`. Missing optional metadata is `null`. Malformed cards produce warnings without invented metadata or discarded records.

Read each entry's `app` as the result of one request to the recorded local address and route, with a 3-second timeout:

- `ok`: the response matches the recorded status and text.
- `stopped`: nothing answers.
- `different`: something else answers, including a non-HTTP listener.
- `null`: the app answered too slowly, the recorded address or route is unusable, or the record was never fully published. A dev server may answer too slowly while recompiling.

Check for a different app now reachable at an old preview link.

Unreadable JSON or malformed cards are skipped with a warning on stderr and retained for recovery.

</details>

## Source-checked CLI mechanics

Use only completely free approved slots, including when this publisher owns an occupied port. Do not claim global atomicity or safe concurrent mutation by noncooperating tools.

<details>
<summary>Reviewed CLI behavior</summary>

These mechanics were reviewed against Tailscale v1.104.1 (`cmd/tailscale/cli/serve_v2.go` and `ipn/serve.go`), not in a live publication or macOS test.

Stored card `Text` is a JSON string containing the complete card, without `text:`. The helper passes `text:` plus encoded JSON as one argv element, never through shell interpolation.

Do not infer from App Store filesystem restrictions or remote-proxy warnings that text cards or literal-loopback proxies are unsupported.

Background root/card additions preserve unrelated handlers, but ordinary Serve can clear Funnel.

The helper removes `--set-path=/.well-known/tailhome.json off`, then `--set-path=/ off`, each with the recorded explicit `--https` port. Never omit `--set-path`; without it, `off` removes every mount on the port. Removing only root leaves the card. Cleanup rereads state because removing a missing handler produces an error.

The advisory lock covers only this helper; external Serve writers can race its reads and writes. The helper refuses unknown schemas and Service Tun mode.

</details>

## Troubleshooting boundaries

Match the symptom before making changes. Keep host validation and certificate verification so a wrong host or untrusted endpoint cannot appear healthy. Never use Funnel, public exposure, whole-port removal, `serve reset`, or tailnet identity, policy or approval changes as a workaround.

| Symptom | Check or owner action |
| --- | --- |
| `serve config denied` | Follow the operator-permission fix in [Read preflight results](#read-preflight-results). |
| Slow first HTTPS check or certificate failure | Issuance may still be running. Retry after the [first-check wait](#verification-and-home-confirmation); check Python CA trust. |
| Phone or laptop cannot find the name | Check its Tailscale connection and MagicDNS use. |
| Browser timeout | Ask an administrator to grant approved device access to this machine and port. |
| 502 or wrong app | Check whether the upstream stopped or moved; mappings outlive apps. Plain `list` reports each preview's `app` result. |
| Dev server rejects the preview host with 403 or "Blocked request" | Add the exact preview hostname to the framework's allowlist, such as Vite [`server.allowedHosts`](https://vite.dev/config/server-options.html#server-allowedhosts) or Next.js [`allowedDevOrigins`](https://nextjs.org/docs/app/api-reference/config/next-config-js/allowedDevOrigins). |
| Direct link works, but `home/` omits it | Ask the person running tailhome to check `TAILHOME_PREVIEW_PORTS` and refresh confirmation. |
| `Waiting for another tailnet-preview command on this account to finish...` on stderr | Another helper command (publish, list, status, check or cleanup) from this OS account holds the lock. Let it finish; do not delete the lock file. |

<details>
<summary>Redirects and unsafe workarounds</summary>

Fix exact-FQDN allowlists and HMR through supported project settings. Inspect redirects within the intended HTTPS origin and explicitly verify the target. Treat arbitrary catalog URLs as data, not authorized probe destinations.

A 502 or Host 403 fails the app check even when TLS works. Do not treat a home listing as proof of app health; home checks connectivity, not pages.

</details>
