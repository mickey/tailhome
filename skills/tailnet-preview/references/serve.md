# Serve reference and installation

## Installation and updates

This is an independent skill by tailhome, not an official Tailscale product.
Install the whole reviewed `skills/tailnet-preview/` directory on the machine hosting the app, where the agent can reach its loopback server and connected Tailscale CLI. Use your client's documented skill directory and activation method. `~/.agents/skills` is a shared location supported by the clients listed below, not a universal discovery path. For other clients, load this SKILL.md and its bundled references using that client's documented mechanism; keep the helper beside it and resolve its absolute path. Do not claim an untested client was validated.

The shared root has documented support in omp, Codex, OpenCode and Gemini CLI.
Add a `~/.claude/skills/tailnet-preview` symlink to that directory for Claude Code.
Do not overwrite an existing installation or link without reviewing its owner and target.
Project-scoped equivalents are `.agents/skills/` and `.claude/skills/`.
An omp-native alternative is `~/.omp/agent/skills/tailnet-preview/` or `.omp/skills/tailnet-preview/`.

Alternative installer: `npx skills add mickey/tailhome --skill tailnet-preview -g`.
Select installed clients interactively, or select `-a claude-code -a codex` explicitly.
The installer requires Node; the published helper itself requires only Python 3.9+ and Tailscale.
There is no `-a omp` target. Selecting `codex` selects the shared directory, not a required harness.
`-a universal -g` writes `~/.config/agents/skills`, not `~/.agents/skills`.
A shared folder is not automatic discovery by every client. Reload/restart the actual client.
Claude Code uses `/tailnet-preview`; Codex supports `$tailnet-preview` or `/skills`;
omp supports `/skill:tailnet-preview` when skill commands are enabled.
Update the full directory from reviewed/pinned source, preserving one canonical copy and its links.
Keep private ownership state outside that installation. Do not distribute state with the skill.

The helper needs Python 3.9+ on Linux or macOS, not native Windows. Check `python3 --version` in the environment your agent uses. If Python is missing, install a current Python 3 yourself using python.org or your existing package manager, then restart the agent. Node is needed only for the optional npx installer. With the python.org macOS installer, run `Install Certificates.command` if HTTPS certificate verification reports an untrusted certificate. Keep certificate verification enabled. WSL publication has not been established; do not assume Windows localhost and a WSL Tailscale daemon are interchangeable.

Use `python3 /absolute/skill/scripts/tailnet_preview.py ...` as documented. If you already use uv, `uv run scripts/tailnet_preview.py ...` also works from the skill directory. The helper declares Python 3.9+ and has no Python package dependencies.

The App Store app bundles its CLI at `/Applications/Tailscale.app/Contents/MacOS/Tailscale`. The helper uses `TAILSCALE`, then an executable on PATH, then this macOS app path. A supplied invalid `TAILSCALE` fails instead of falling back. The value is one executable path or name, never a shell command or argv fragment. A shell alias is not an executable. Set `TAILSCALE=/Applications/Tailscale.app/Contents/MacOS/Tailscale` in the environment used to launch your agent if needed. The helper sets `TAILSCALE_BE_CLI=1` for macOS subprocesses without changing its parent environment. Dry runs show the selected executable. For the standalone app, Settings > CLI integration can install `/usr/local/bin/tailscale`. Restart GUI-launched agents after changing their environment. Use the variant already installed; do not install another daemon or switch variants just for this skill.

See the official [CLI documentation](https://tailscale.com/docs/reference/tailscale-cli) and [macOS variants](https://tailscale.com/docs/concepts/macos-variants).

Run `preflight` before the approval question. It reads CLI version, status, optional Linux operator preferences and Serve configuration without creating ownership state or changing Tailscale. `--ports` optionally reports free slots using publication's eligibility rules; inspecting a pool does not approve it. Exit status is nonzero only for a missing CLI, disconnected or logged-out backend, or unavailable HTTPS. Other uncertain checks appear in warnings. Certificate-domain or MagicDNS evidence is a readiness signal, not a live certificate issuance test.

On Linux, root or the configured `OperatorUser` is likely able to write Serve. Other users receive `serve_write:"denied_likely"` and the owner-side fix `sudo tailscale set --operator=$USER`, run once by the owner. Unreadable preferences and macOS GUI variants report `unknown`, not permission success. Include permission problems and other blockers in the single approval question. Access-denied Serve errors include the same remediation; the helper does not elevate itself.

## Host, approval and direct links

Serve readiness is separate from CLI connectivity. A tailnet administrator must enable MagicDNS and HTTPS certificates, and policy must permit the intended viewers to reach the approved ports. If Serve returns a consent URL or permission error, preserve the actual stderr and ask the user or administrator to complete approved setup, then retry. Do not authenticate, provision, elevate or change policy.

`--home` or trusted `TAILHOME_URL` is optional on publish, status and check. Omit it for direct-link-only previews. Do not search for or install tailhome. Its operator separately configures `TAILHOME_PREVIEW_PORTS` for extra-port discovery. Different hosts can reuse ports; different pools need their union of at most 32 extra ports in the launcher, not globally unique port numbers. Publication approval, tailnet access policy and launcher discovery are separate contracts.

A nonempty `TAILHOME_DEVICE_TAGS` filter excludes untagged laptops. An empty filter includes untagged devices too. Do not suggest tagging a personal laptop just for discovery: tagging replaces its user identity and changes applicable policy. See [preview setup](../../../docs/setup.md#preview-ports).

Connect Tailscale on the phone or review device before opening the link. Reviewers need approved access to the publishing machine and HTTPS port. For someone outside your tailnet, arrange an administrator-approved device share or invitation; this skill does not grant access or offer a public fallback. Host-side verification does not prove viewer connectivity or policy. Keep the publishing computer awake, connected and running the app. A background Serve mapping does not host or restart it.

## Card schema

The origin serves `/.well-known/tailhome.json`, also reserving its slash-delimited descendants.
UTF-8 body limit: 4096 bytes. Exactly one non-null JSON object, trailing whitespace only.
Duplicate keys, including unknown keys, are invalid. Unknown fields otherwise are ignored.
Optional strings: `title` 60 code points, `description` 140, `set` 60;
`icon` at most 64 ASCII characters matching `[a-z0-9-]+`;
`version` one or two ASCII letters/digits. `preview` is boolean, never null.
Trim Unicode whitespace before checking strings; empty strings are absent.
Reject remaining Unicode controls and U+2028/U+2029. Invalid known fields invalidate the card.
The helper always writes `preview:true`. Sets match exact trimmed text and are host-scoped.
Cards do not authorize discovery ports, access, redirects, expiry or cleanup.
Content-Type is deliberately ignored: Serve text mounts return `text/plain; charset=utf-8`.

Use short, user-facing comparison names such as `Lumen pricing` or `Lumen heroes`. Titles describe approaches, descriptions explain differences and inspection tasks, and version badges identify alternatives rather than results. Do not expose workspace directories, QA labels or evaluation iteration numbers in titles, descriptions or set names unless the user asked reviewers to see them. Keep run uniqueness in local non-secret IDs; use meaningful user-facing qualifiers when unrelated comparisons need distinct set names.


## Verification and home confirmation

App probes read at most the first 1 MiB, so larger or streaming pages can pass by expected status. Choose identifying `--expect-text` within that prefix. Card probes read at most 4097 bytes to enforce the 4096-byte limit. Requests follow no redirects and use normal certificate verification.

For exact Self or Peer DNS names and service DNS names in `ExtraRecords` from one cached `tailscale status --json` snapshot, the helper sends HTTP/1.1 through the selected CLI's `tailscale nc` transport. TLS uses the URL FQDN for SNI and certificate hostname verification. This works with both TUN and userspace networking, without OS MagicDNS resolution. Each request has an overall deadline and always terminates and reaps its transport subprocess. Other hosts keep the normal urllib transport.

The first HTTPS card check retries transport failures for up to 90 seconds while tailscaled obtains its certificate. Python must trust public certificate authorities; python.org macOS builds may need `Install Certificates.command`. Keep certificate verification enabled.

Verification failures retain the specific check failure in both `error` and `warnings`. Probe warnings preserve the original stage, route, transport, elapsed time and underlying error, including certificate or transport failures. Report those details rather than replacing them with a generic "see warnings"; keep TLS verification and authority boundaries intact.

Failed publish results also expose `compensation_completed` and `ownership_record_remains`. On required-publication failure, compensation removes only unchanged owned mounts. Report that version incomplete, whether compensation completed and whether its ownership record remains for recovery. Retained state is not a successful preview. Preserve independent successful siblings and their app sessions. Status checks do not remove mounts, and compensation does not authorize app shutdown.

With a trusted home URL, the helper makes at most three refresh attempts, at least eleven seconds apart after the first, with eight-second request timeouts. A refresh POST can time out while the server continues; the helper still checks the catalog. The launcher and publishing machine clocks must agree within a few seconds for timestamp confirmation.

The helper checks `/api/services.json` for a fresh `updated` timestamp and no catalog error, and `/api/list.json` for the exact URL, label, description, set and version. Both freshness and matching metadata are required. A 303 alone proves neither: the shared cooldown may skip card work. Failure to confirm listing leaves a verified direct preview usable.

## Local state and set selectors

`XDG_STATE_HOME` must be absolute. Default ownership state is `~/.local/state/tailnet-preview/`, outside the installed skill, in private mode-0700 directories and mode-0600 files. The helper writes intent before mutation, updates it after observed effects, and compensates only unchanged owned mounts after failure.

Ownership records and the helper lock belong to this OS account's state directory, not to a Tailscale user or the whole team. Agents sharing that directory need unique IDs and distinct set names for unrelated comparisons. Different machines may reuse IDs and set names. Separate OS accounts sharing one Tailscale daemon do not share this lock: serialize publishing externally or use disjoint approved pools. Each account can list and clean only its recorded publications. tailhome is not a team cleanup registry. Other Serve tools are outside this lock.

The approved pool accepts comma-separated decimal ports/ranges, at most 32 distinct ports, including explicitly approved 443. It has no built-in default. The example `8443-8452` contains ten host-wide slots before occupied/ineligible ports are excluded.

Plain `list` returns local ownership records without requiring `--set` or a remote catalog. Every `previews[]` entry preserves all original ownership-record fields, including the raw `card` used for recovery, and adds top-level `title`, `description`, `set` and `version` decoded from that recorded card. Absent optional metadata is `null`. Malformed cards produce warnings without invented metadata or discarded records. These fields describe local recorded metadata, not catalog-derived membership.

`list --set NAME` validates and trims the selector and matches the recorded card's exact case-sensitive set. Its entries retain the same raw fields and derived metadata as plain `list`. Records with unreadable JSON or malformed cards are skipped with a warning on stderr and retained for recovery.
`cleanup --id ID` and `cleanup --set NAME` are mutually exclusive. Set cleanup snapshots selected local records under the helper lock, checks current node identity/FQDN, and applies the same mount checks as single-ID cleanup. Each result reports its ID, removal outcome and warnings; the aggregate reports the selected count. Empty selection is an explicit no-op. Conflicts produce a nonzero exit while independent members continue. `--dry-run` reports per-ID commands without removal.

Set labels and remote catalogs confer no ownership. A changed member stays recorded for recovery; other sets and foreign mounts remain untouched. Application processes and worktrees are outside cleanup.

## Source-checked CLI mechanics

Reviewed against Tailscale v1.104.1 (`cmd/tailscale/cli/serve_v2.go` and `ipn/serve.go`), not a live publication or macOS result.
Status JSON contains `TCP`, `Web`, `AllowFunnel`, `Foreground`, and `Services` maps.
Web keys are explicit `fqdn:port`; handlers contain `Proxy`, `Text`, `Path` or `Redirect`.
The card's stored `Text` is a JSON string containing the entire JSON card, without `text:`.
The helper passes `text:` plus encoded JSON as a single argv element, never shell interpolation.
The `text:` branch is handled before absolute filesystem targets and is not filesystem serving. Loopback HTTP uses the proxy branch. App Store filesystem sandbox restrictions and remote-proxy warnings do not establish that text cards or literal-loopback proxies are unsupported.
Background root/card additions preserve unrelated handlers, but ordinary Serve can clear Funnel.
Therefore only completely free approved slots are eligible; occupied publisher-owned ports are not.

Path-scoped removal requires `--set-path=/.well-known/tailhome.json off`, then
`--set-path=/ off`, each with the recorded explicit `--https` port.
Without `--set-path`, `off` removes every mount on the port. Root-only removal leaves the card.
Removing the final handler removes its listener. Missing-handler removal errors, so cleanup rereads.
The advisory lock covers this helper only. External Serve writers retain a read/write race;
do not claim global atomicity or support concurrent noncooperating mutation.
The helper conservatively refuses unknown schemas and Service Tun mode rather than guessing.

## Troubleshooting boundaries

Fix exact-FQDN allowlists and HMR through the project's supported settings. Retain host validation and certificate verification: bypasses can make the wrong host or an untrusted endpoint look healthy.

Inspect redirects within the intended HTTPS origin and verify the target explicitly. Arbitrary catalog URLs are data, not approved probe destinations. A 502 or Host 403 is a failed app check even when TLS works. Home may still list the listener because catalog discovery does not establish app health.
