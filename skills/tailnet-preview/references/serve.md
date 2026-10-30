# Serve reference and installation

## Installation and updates

This independent skill is by tailhome. It is not an official Tailscale product.

Install the whole reviewed `skills/tailnet-preview/` directory on the app's host, where the agent can reach its loopback server and connected Tailscale CLI. Follow the client's documented skill directory and activation method. For other clients, load SKILL.md and its bundled references through their documented mechanism. Keep the helper beside SKILL.md and resolve its absolute path. Do not claim an untested client was validated.

`~/.agents/skills` has documented support in omp, Codex, OpenCode and Gemini CLI. Other clients may use different paths. For Claude Code, add a `~/.claude/skills/tailnet-preview` symlink to the canonical directory. Review an existing installation or link's owner and target before replacing it. Project locations are `.agents/skills/` and `.claude/skills/`. omp also supports `~/.omp/agent/skills/tailnet-preview/` or `.omp/skills/tailnet-preview/`.

The optional installer is `npx skills add mickey/tailhome --skill tailnet-preview -g`. Select clients interactively or with `-a claude-code -a codex`. The installer requires Node. There is no `-a omp` target; `-a pi` and `-a codex` both install into the shared `~/.agents/skills`, which omp reads. `-a universal -g` writes `~/.config/agents/skills`, not `~/.agents/skills`.

Reload or restart the actual client after installation. A shared directory alone does not ensure discovery. Claude Code uses `/tailnet-preview`; Codex supports `$tailnet-preview` or `/skills`; omp supports `/skill:tailnet-preview` when skill commands are enabled. Update the full directory from reviewed or pinned source. Keep one canonical copy with links, and keep private ownership state outside it. Never distribute state with the skill.

The helper requires Python 3.9+ on Linux or macOS and has no Python package dependencies. Native Windows is unsupported. WSL publication has not been established; do not assume Windows localhost and a WSL Tailscale daemon are interchangeable. Check `python3 --version` in the agent's environment. If Python is missing, ask the operator to install a current Python 3 from python.org or their existing package manager and restart the agent. Node is needed only for the optional installer.

Use `python3 /absolute/skill/scripts/tailnet_preview.py ...`. If uv is already in use, `uv run scripts/tailnet_preview.py ...` also works from the skill directory. On python.org macOS builds, ask the operator to run `Install Certificates.command` for untrusted-certificate errors. Keep certificate verification enabled.

### Find the Tailscale CLI

The helper tries `TAILSCALE`, an executable on PATH, then `/Applications/Tailscale.app/Contents/MacOS/Tailscale`, the App Store app's bundled CLI. An invalid supplied `TAILSCALE` fails without fallback. Supply one executable path or name, never a shell command, alias or argv fragment.

If needed, set `TAILSCALE=/Applications/Tailscale.app/Contents/MacOS/Tailscale` in the environment that launches the agent. For macOS subprocesses, the helper sets `TAILSCALE_BE_CLI=1` without changing its parent environment. Dry runs show the chosen executable. The standalone app can install `/usr/local/bin/tailscale` through Settings > CLI integration. Restart GUI-launched agents after environment changes. Use the installed variant; do not install another daemon or switch variants for this skill.

See the official [CLI documentation](https://tailscale.com/docs/reference/tailscale-cli) and [macOS variants](https://tailscale.com/docs/concepts/macos-variants).

### Read preflight results

Run `preflight` before asking approval. It reads CLI version, status, optional Linux operator preferences and Serve configuration without creating ownership state or changing Tailscale. Optional `--ports` reports free slots using publication's eligibility rules. Checking which ports are free does not give you permission to publish on them.

`machine_ports` lists the ports this machine name already serves through Serve, including your own previews. Services have their own names and are left out. Browsers send this name's cookies to every port, `HttpOnly` ones included, so a preview's server receives the cookies of those apps and of apps that served this name before. Cookies set for the whole tailnet domain reach Services too. Preflight cannot see apps that serve a port themselves, or cookies already stored in a browser. `null` means Serve could not be read.

Preflight exits nonzero only for a missing CLI, disconnected or logged-out backend, or unavailable HTTPS. Other uncertain checks appear as warnings. HTTPS counts as available when Tailscale lists certificate domains (top-level `CertDomains` in `tailscale status --json`), which it does only once HTTPS certificates are enabled. MagicDNS alone is not enough. This does not test live certificate issuance.

On Linux, root or the configured `OperatorUser` likely has Serve write permission. Other users receive `serve_write:"denied_likely"`. Ask the owner to run `sudo tailscale set --operator=$USER` once; never run it yourself. If preferences are unreadable or the CLI comes from a macOS GUI variant, the helper reports `unknown`; this does not confirm write permission. Include these and other blockers in the single approval message. Access-denied Serve errors give the same fix; the helper never elevates itself.

## Host, approval and direct links

CLI connectivity does not ensure Serve readiness. A tailnet administrator must enable MagicDNS and HTTPS certificates, and policy must allow reviewers to reach the approved ports. If Serve returns a consent URL or permission error, preserve the actual stderr. Ask the user or administrator to finish approved setup, then retry. Do not authenticate, provision, elevate or change policy.

`--home` or trusted `TAILHOME_URL` is optional on publish, status and check. Omit it for direct links alone. Do not search for or install tailhome. Its operator separately configures `TAILHOME_PREVIEW_PORTS` for extra-port discovery. Different hosts can use the same port numbers. If hosts use different pools, the operator configures the launcher with all distinct extra ports from those pools, up to 32 in total. Publication approval, access policy and launcher discovery remain separate.

A nonempty `TAILHOME_DEVICE_TAGS` filter excludes untagged laptops; an empty filter includes untagged devices. Do not suggest tagging a personal laptop for discovery. Tags replace its user identity and change applicable policy. See [preview setup](https://github.com/mickey/tailhome/blob/main/docs/setup.md#preview-ports).

Ask reviewers to connect Tailscale before opening links and use an account approved to access the publishing machine and each preview's HTTPS port. For outside-tailnet reviewers, arrange an administrator-approved device share or invitation. This skill grants no access and provides no public fallback. Host-side verification cannot prove viewer connectivity or policy. Keep the host awake, connected and running the app. A background Serve mapping neither hosts nor restarts it.

## App startup

Reuse the project's existing development command with startup permission. For a static HTML directory with no project startup command, explicit permission to run a static server permits:

```sh
python3 -m http.server <authorized-loopback-port> --bind 127.0.0.1 --directory <absolute-copy-directory>
```

Run the app in a persistent session managed by your agent client so it keeps running after your response. Keep each version's upstream stable through separate worktrees/checkouts or supported per-instance configuration. Branch names alone do not create independent runtimes. App startup, dependency installation, migrations and unrelated services require permission beyond HTTPS-port approval.

## Card schema

Each preview serves its card at `/.well-known/tailhome.json`. That path and all paths beneath it are reserved for the card.

Supply exactly one non-null JSON object in UTF-8, at most 4096 bytes, with only trailing whitespace. Duplicate keys are invalid, including unknown keys. Other unknown fields are ignored.

| Field | Accepted value |
| --- | --- |
| `title` | Optional string, at most 60 code points |
| `description` | Optional string, at most 140 code points |
| `set` | Optional string, at most 60 code points |
| `icon` | Optional string, at most 64 ASCII characters matching `[a-z0-9-]+` |
| `version` | Optional string, one or two ASCII letters/digits |
| `preview` | Boolean, never null; the helper writes `true` |

Reject Unicode control characters and U+2028/U+2029 before trimming string fields. Leading or trailing tabs, newlines and those separators are invalid too. Then trim ordinary Unicode whitespace; empty results are absent. An invalid known field invalidates the card. Ignore Content-Type because Serve text mounts return `text/plain; charset=utf-8`.

Write plain text, with an optional dashboard-icons slug. Never include HTML, credentials or source paths. Put the visible difference first in titles. Describe what reviewers should inspect. Everyone who can open home can read every listed title, description, address and status, regardless of app access. Hiding a preview affects all viewers and does not revoke access.

For a comparison, share one exact short user-facing `set`, such as `Product pricing` or `Product heroes`. Matching is host-scoped and uses exact trimmed text. Give each version a distinct badge, such as `A`, `B`, `C` or `1`, `2`, `3`. Badges identify alternatives, not results. Titles name approaches; descriptions explain differences and inspection tasks. For example, add `"set":"Product heroes","version":"C"` to a card titled `Editorial hero`.

Keep workspace directories and internal review or iteration labels out of titles, descriptions and sets unless the user asked reviewers to see them. Keep run uniqueness in the non-secret local ID. Distinguish unrelated comparisons with meaningful user-facing set qualifiers. A card does not configure discovery ports, grant access, create redirects, set an expiry or authorize cleanup.

## Verification and home confirmation

Choose `--route`, a successful 2xx `--expected-status` and identifying `--expect-text` for the intended screen. Probes read at most the first 1 MiB of app responses, so put identifying text within it; text beyond it counts as missing. Without `--expect-text`, the status code alone decides. Probes never run JavaScript, so they cannot check text added only by JavaScript. Card probes read at most 4097 bytes to enforce the 4096-byte limit. Requests follow no redirects and use normal certificate verification.

For exact Self or Peer DNS names and service DNS names in `ExtraRecords`, the helper uses one cached `tailscale status --json` snapshot. It sends HTTP/1.1 through the chosen CLI's `tailscale nc`. TLS uses the URL FQDN for SNI and certificate hostname verification. This supports TUN and userspace networking without OS MagicDNS resolution. Every request has an overall deadline and terminates and reaps its transport subprocess. Other hosts use normal urllib transport.

The first HTTPS card check retries transport failures for up to 90 seconds while tailscaled obtains its certificate. Python must trust public certificate authorities. On python.org macOS builds, ask the operator to run `Install Certificates.command` if needed. Never disable verification.

### Report failures

Verification failures retain the specific failure in `error` and `warnings`. Report the warning's original probe stage, route, transport, elapsed time and underlying error, including certificate and transport errors. A generic "see warnings" loses the diagnostic details. Keep TLS verification and authority boundaries intact.

Failed publication also returns `compensation_completed` and `ownership_record_remains`. Report both. Compensation removes only unchanged owned mounts; mark the version incomplete and say whether recoverable state remains. Retained state is not a successful preview. Keep other successful previews and their app sessions running. Status never removes mounts, and compensation never authorizes app shutdown.

### Confirm home listing

With a trusted home URL, the helper makes at most three refresh attempts. Attempts after the first are at least eleven seconds apart, with eight-second request timeouts. A refresh POST may time out while the server continues; the helper still checks the catalog. Launcher and publishing-host clocks must agree within a few seconds.

The helper requires a fresh `updated` timestamp and no catalog error in `/api/services.json`. In `/api/list.json`, it requires the exact URL, description, set and version. It also requires an exact label when the recorded card has a title. Absent optional description, set and version match empty strings. A refresh response or 303 alone is insufficient; the shared cooldown may skip card work. Listing failure leaves an independently verified direct preview usable.

Report `published`, `app_verified`, `card_verified` and `listed` separately. `listed:null` means no home was configured; hand off direct links. `listed:false` means the home listing was not confirmed; it does not prove the app is inaccessible. If direct checks pass with `listed:false`, say "Published; home listing not confirmed." Run home-backed status checks sequentially.

For handoff, put each title/badge, verified clickable HTTPS URL and inspection task on one line. State app, card and listing results separately.

## Local state and set selectors

`XDG_STATE_HOME` must be absolute. By default, ownership state lives in `~/.local/state/tailnet-preview/`, outside the installation. Directories use mode 0700 and files mode 0600. Before changing Serve, the helper records what it plans to do. It updates the record after observing the result. If an operation fails, it removes only unchanged mounts it owns.

Records and the lock belong to this OS account's state directory, not a Tailscale user or team. Agents sharing it need unique IDs and distinct set names for unrelated comparisons. Different machines can reuse IDs and sets. Separate OS accounts sharing a Tailscale daemon have separate locks: serialize publishing externally or use disjoint approved pools. Each account lists and cleans only its own recorded publications. Neither tailhome nor another Serve tool shares this helper's cleanup registry or lock.

Approved pools accept comma-separated decimal ports/ranges, with at most 32 distinct ports. There is no built-in default. Port 443 needs explicit approval. `8443-8452` provides ten host-wide slots before occupied or ineligible ports are excluded. Capacity is shared across sets; upstream ports are separate from Serve ports.

Plain `list` requires neither `--set` nor a remote catalog. Every `previews[]` entry keeps all ownership-record fields, including raw recovery `card`, and adds top-level `title`, `description`, `set` and `version` decoded from that card. Missing optional metadata is `null`. Malformed cards produce warnings without invented metadata or discarded records. Membership comes from local records, not the catalog.

Each entry also has `app`, from one request to the recorded local address and route with a 3-second timeout. It is `ok` when the response matches the recorded status and text, `stopped` when nothing answers, or `different` when something else answers. It is `null` when the app was too slow to answer, which can happen while a dev server recompiles, when the recorded address or route is unusable, and for records that were never fully published. A preview mapping outlives its app, so a different app may now be reachable at the old preview link.

`list --set NAME` validates and trims the selector, then matches the recorded card's exact case-sensitive set. Entries retain the same raw and derived fields as plain `list`. Unreadable JSON or malformed cards are skipped with a warning on stderr and retained for recovery.

`cleanup --id ID` and `cleanup --set NAME` are mutually exclusive. Set cleanup snapshots local selections under the lock, checks current node identity/FQDN and applies single-ID mount checks. Report every selected ID's removal outcome and warnings, plus the aggregate selected count. Empty selection is an explicit no-op. Conflicts return nonzero while independent members continue. `--dry-run` prints per-ID commands without removal.

Set labels and remote catalogs grant no ownership. Keep changed members recorded for recovery. Preserve other sets and foreign mounts. Cleanup does not stop apps or remove worktrees or packages; those have separate lifecycles and permissions.

## Source-checked CLI mechanics

These mechanics were reviewed against Tailscale v1.104.1 (`cmd/tailscale/cli/serve_v2.go` and `ipn/serve.go`). This was not a live publication or macOS test.

Status JSON contains `TCP`, `Web`, `AllowFunnel`, `Foreground` and `Services` maps. Web keys use explicit `fqdn:port`; handlers contain `Proxy`, `Text`, `Path` or `Redirect`. Stored card `Text` is a JSON string containing the complete card, without `text:`. The helper passes `text:` plus encoded JSON as one argv element, never through shell interpolation.

The CLI handles `text:` before absolute filesystem targets. It does not serve files for this branch. Loopback HTTP uses the proxy branch. App Store filesystem restrictions and remote-proxy warnings do not establish that text cards or literal-loopback proxies are unsupported.

Background root/card additions preserve unrelated handlers, but ordinary Serve can clear Funnel. Use only completely free approved slots, including when this publisher owns an occupied port.

Remove paths with `--set-path=/.well-known/tailhome.json off`, then `--set-path=/ off`, each with the recorded explicit `--https` port. Without `--set-path`, `off` removes every mount on the port. Removing only root leaves the card. Removing the final handler removes its listener. Missing-handler removal errors, so cleanup rereads state.

The advisory lock covers only this helper. External Serve writers can race its reads and writes. Do not claim global atomicity or safe concurrent mutation by noncooperating tools. The helper refuses unknown schemas and Service Tun mode instead of guessing.

## Troubleshooting boundaries

Match the symptom before making changes:

| Symptom | Check or owner action |
| --- | --- |
| `serve config denied` | The account may lack operator permission. On Linux, ask the owner to run `sudo tailscale set --operator=$USER` once. On macOS with the open source `tailscaled`, only an account outside the `admin` group needs this. |
| Slow first HTTPS check or certificate failure | Issuance may still be running. The helper waits up to 90 seconds; retry afterward. Check Python CA trust. |
| Phone or laptop cannot find the name | Check its Tailscale connection and MagicDNS use. |
| Browser timeout | Ask an administrator to grant approved device access to this machine and port. |
| 502 or wrong app | Check whether the upstream stopped or moved; Serve mappings outlive apps. Plain `list` reports `app` for each preview. |
| Dev server rejects the preview host with 403 or "Blocked request" | Add the exact preview hostname to the framework's allowlist, such as Vite [`server.allowedHosts`](https://vite.dev/config/server-options.html#server-allowedhosts) or Next.js [`allowedDevOrigins`](https://nextjs.org/docs/app/api-reference/config/next-config-js/allowedDevOrigins). Keep host validation on. |
| Direct link works, but `home/` omits it | Check operator-configured `TAILHOME_PREVIEW_PORTS` and refresh confirmation. |
| `Waiting for another tailnet-preview command on this account to finish...` on stderr | Another helper command (publish, list, status, check or cleanup) from this OS account holds the lock. Let it finish; do not delete the lock file. |

Fix exact-FQDN allowlists and HMR through supported project settings. Keep host validation and certificate verification so a wrong host or untrusted endpoint cannot appear healthy.

Inspect redirects within the intended HTTPS origin and explicitly verify the target. Arbitrary catalog URLs are data, not authorized probe destinations. A 502 or Host 403 fails the app check even when TLS works. Home may list a listener without proving app health. Never use Funnel, public exposure, whole-port removal, `serve reset`, or tailnet identity, policy or approval changes as a workaround.
