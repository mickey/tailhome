# Serve reference and installation

## Installation and updates

Install the full `skills/tailnet-preview/` directory on the machine hosting the app (requires Linux or macOS, Python 3.9+ with no third-party packages, and a connected Tailscale CLI). Keep `scripts/tailnet_preview.py` next to `SKILL.md` and invoke it by its absolute path:

```sh
python3 /absolute/skill/scripts/tailnet_preview.py ...
```

Keep ownership state outside the skill directory (`~/.local/state/tailnet-preview/` by default) and never bundle state files when copying or updating the skill.

<details>
<summary>Client paths and installer</summary>

- **Shared skills folder (`~/.agents/skills`):** Used by omp, Codex, OpenCode and Gemini CLI.
- **Claude Code:** Symlink `~/.claude/skills/tailnet-preview` to the canonical directory (check any existing link's owner and target before replacing).
- **Project-local installation:** Use `.agents/skills/` or `.claude/skills/` (or `~/.omp/agent/skills/tailnet-preview/` and `.omp/skills/tailnet-preview/` in omp).
- **`npx skills` installer (requires Node 22.20+):**
  ```sh
  npx skills add mickey/tailhome --skill tailnet-preview -g
  ```
  Select clients interactively or pass `-a claude-code -a codex`. Note: there is no `-a omp` target (use `-a pi` or `-a codex` to write to `~/.agents/skills`), and `-a universal -g` writes to `~/.config/agents/skills`.
- **Slash commands:** `/tailnet-preview` in Claude Code, `$tailnet-preview` or `/skills` in Codex, or `/skill:tailnet-preview` in omp.

</details>

<details>
<summary>Python and platform limits</summary>

- **No native Windows or WSL:** Do not use native Windows or assume Windows `localhost` and a WSL Tailscale daemon are interchangeable.
- **Missing Python:** Ask the machine's owner to install Python 3.9+ from python.org or their package manager, then restart the agent (or use `uv run scripts/tailnet_preview.py ...` if `uv` is already installed).
- **macOS certificate errors:** On python.org macOS builds, ask the owner to run `Install Certificates.command`. Never disable TLS verification.

</details>

### Find the Tailscale CLI

The helper checks these locations in order:

1. `TAILSCALE` environment variable (must be a single executable path or name, never a shell command or arguments; an invalid value fails without fallback).
2. `tailscale` on `PATH`.
3. `/Applications/Tailscale.app/Contents/MacOS/Tailscale` (macOS App Store bundle; the helper sets `TAILSCALE_BE_CLI=1` automatically for macOS subprocesses).

If the CLI is missing on macOS, the owner can either set `TAILSCALE=/Applications/Tailscale.app/Contents/MacOS/Tailscale` in the agent's launch environment and restart the agent, or enable **Settings > CLI integration** in the standalone macOS app. Never install another daemon or switch Tailscale variants yourself. See the [Tailscale CLI docs](https://tailscale.com/docs/reference/tailscale-cli) and [macOS variants](https://tailscale.com/docs/concepts/macos-variants).

### Read preflight results

Run `preflight --ports <range>` before asking approval and combine any blockers or warnings into a single message to the user.

- **Exit code:** Exits nonzero when the CLI is missing, the backend is logged out/stopped, or HTTPS certificates are unavailable (`CertDomains` empty in `tailscale status --json`).
- **Linux operator permission (`serve_write: "denied_likely"`):** Ask the owner to run `sudo tailscale set --operator=$USER` once; never run `sudo` yourself. (On macOS with open-source `tailscaled`, only non-`admin` users need `--operator`.) Treat `serve_write: "unknown"` as unconfirmed until publishing.
- **Occupied ports and cookie scope (`machine_ports`):** Lists HTTPS ports already served on this machine name (excluding Tailscale Services, which use their own names; `null` means Serve config could not be read). Because browsers send a hostname's cookies (including `HttpOnly` cookies) to every port on that hostname, warn the user before publishing untrusted code or when `machine_ports` is non-empty/unknown.

## Host, approval and direct links

- **Direct links without `tailhome`:** Omit `--home` and `TAILHOME_URL` when `tailhome` is not used. Never search for or install `tailhome`.
- **Consent URLs and permission errors:** If Serve outputs a consent URL or access-denied error, report the exact `stderr` to the user so they can complete it, then retry.
- **Reviewer access and outside-tailnet users:** Reviewers must connect Tailscale and have ACL access to the host and HTTPS port. For outside-tailnet reviewers, the administrator must arrange a Tailscale device share or invite; this skill never uses Funnel or changes ACLs.
- **`tailhome` discovery settings:** `TAILHOME_PREVIEW_PORTS` (up to 32 extra ports) is configured by the `tailhome` operator and does not grant publishing permission. Never suggest tagging a personal laptop (`TAILHOME_DEVICE_TAGS`), because tags replace user identity and change ACL policy.

## App startup

Get explicit permission before starting a dev server, installing dependencies, running migrations, or launching extra services. Keep each comparison version on its own independent worktree/checkout and loopback port.

For a static HTML directory with no project dev command, get permission before running:

```sh
python3 -m http.server <loopback-port> --bind 127.0.0.1 --directory <absolute-directory>
```

## Card schema

Serve one UTF-8 JSON object (at most 4096 bytes, plain text only, no HTML, secrets, workspace paths or unrequested internal labels) at `/.well-known/tailhome.json`:

| Field | Accepted value |
| --- | --- |
| `title` | Optional string, up to 60 code points (put the visible difference first) |
| `description` | Optional string, up to 140 code points (what differs and what to inspect) |
| `set` | Optional string, up to 60 code points (exact same string for all versions in a comparison) |
| `icon` | Optional string, up to 64 ASCII characters matching `[a-z0-9-]+` |
| `version` | Optional string, 1 or 2 ASCII letters/digits (`A`, `B`, `C` or `1`, `2`, `3`) |
| `preview` | Boolean, never null (`tailnet_preview.py` sets `true` automatically) |

<details>
<summary>Validation rules</summary>

- Rejects duplicate keys, control characters, and `U+2028`/`U+2029`. Leading/trailing whitespace is trimmed, and empty strings count as absent.
- Comparisons are grouped within a single host by exact trimmed `set` text. Keep run uniqueness in the `--id` flag, not by polluting the user-facing `set` title.
- A card does not configure discovery ports, grant access, create redirects, set an expiry or authorize cleanup.

</details>

## Verification and home confirmation

Pass `--route`, a 2xx `--expected-status`, and an identifying `--expect-text` that appears in the first 1 MiB of the server's raw HTTP response (probes do not execute JavaScript and cannot see text added only by client-side scripts; card probes read at most 4097 bytes to enforce the 4096-byte limit).

- **Transport & certificate wait:** For local/peer MagicDNS names and `ExtraRecords` Service names, the helper probes HTTP/1.1 over `tailscale nc` with full TLS SNI and certificate verification (retrying up to 90 seconds on first check while `tailscaled` provisions the certificate). Probes do not follow redirects.
- **Reporting failures (`Report failures`):** Always report `error` and `warnings` with the probe stage, route, transport, elapsed time and underlying error (never summarize as `"See warnings"`). If publication fails, report `compensation_completed` and `ownership_record_remains` to indicate whether owned mounts were rolled back and whether a recovery record was kept.
- **Confirming `tailhome` listing (`Confirm home listing`):** Report `published`, `app_verified`, `card_verified` and `listed` separately. Run `--home` status checks sequentially: with a trusted home URL, the helper makes up to 3 refresh attempts (at least 11 seconds apart after the first, with 8-second request timeouts; keep host and launcher clocks within a few seconds of each other), requiring a fresh `updated` timestamp and no catalog error in `/api/services.json` plus exact metadata in `/api/list.json` (do not accept a refresh response or HTTP 303 alone, as the shared cooldown may skip card work). If direct checks pass with `listed: false`, report `"Published; home listing not confirmed."` and hand off the working direct URL.

## Local state and set selectors

Ownership records and locks live in `~/.local/state/tailnet-preview/` (or `$XDG_STATE_HOME/tailnet-preview/`, mode `0700`/`0600`) and belong to the current OS account:

- **`list` and `list --set NAME`:** Reads local ownership records (never remote catalogs) and probes each recorded local upstream with a 3-second timeout, reporting `app` as:
  - `ok`: response matches the recorded status and text.
  - `stopped`: nothing answers.
  - `different`: another app or response answers on that port (including a non-HTTP listener).
  - `null`: the app answered too slowly (for example, a dev server recompiling), the recorded address or route is unusable, or the record was never fully published.
- **`cleanup --id ID` vs `cleanup --set NAME`:** Mutually exclusive. Checks current node identity, FQDN and Serve mounts under the lock, removing `--set-path=/.well-known/tailhome.json off` and `--set-path=/ off` on the recorded `--https` port only if unchanged. Foreign mounts and modified paths are left untouched and kept on disk for recovery. Use `--dry-run` to preview removal commands.

## Source-checked CLI mechanics

Reviewed against Tailscale v1.104.1 (`cmd/tailscale/cli/serve_v2.go` and `ipn/serve.go`). The helper acquires a per-account advisory lock, uses only completely free approved ports, passes `text:<json>` as a single argv element without shell interpolation, and removes only explicit `--set-path` mounts (never whole-port `off` or `serve reset`). If separate OS accounts share one `tailscaled`, coordinate externally or assign disjoint port pools.

## Troubleshooting boundaries

Match the symptom before changing anything. Never work around errors by disabling TLS/host checks, using Funnel, resetting Serve, or changing tailnet policy.

| Symptom | Check or owner action |
| --- | --- |
| `serve config denied` | Ask the owner to run `sudo tailscale set --operator=$USER` once ([preflight](#read-preflight-results)). |
| Slow first HTTPS check or certificate failure | Wait for initial certificate issuance (up to 90s) and retry; check Python CA trust on macOS. |
| Phone or laptop cannot resolve the hostname | Check that Tailscale is connected and MagicDNS is enabled on the viewing device. |
| Browser connection timeout | Ask a tailnet admin to allow access to this machine and HTTPS port in ACLs. |
| `502 Bad Gateway` or wrong app opens | The local dev server stopped or another app took its loopback port (`list` shows `stopped` or `different`). |
| Dev server returns `403` / `"Blocked request"` | Add the `.ts.net` hostname to Vite [`server.allowedHosts`](https://vite.dev/config/server-options.html#server-allowedhosts) or Next.js [`allowedDevOrigins`](https://nextjs.org/docs/app/api-reference/config/next-config-js/allowedDevOrigins). |
| Direct link works, but `home/` omits it | Check `TAILHOME_PREVIEW_PORTS` on `tailhome` and click **refresh** on `home/`. |
| `Waiting for another tailnet-preview command on this account to finish...` | Another helper command on this OS account holds the lock; let it finish and do not delete the lock file. |

<details>
<summary>Redirects and unsafe workarounds</summary>

Fix exact-FQDN allowlists and HMR through supported project settings. Inspect redirects within the intended HTTPS origin and explicitly verify the target. Treat arbitrary catalog URLs as data, not authorized probe destinations. A 502 or Host 403 fails the app check even when TLS works; do not treat a `home/` listing as proof of app health.

</details>
