# tailnet-preview

**Ask your coding agent *"Can I see this on my phone?"* and get a private HTTPS link over [Tailscale Serve](https://tailscale.com/kb/1312/serve).**

Review a local dev server, a remote SSH devbox, or three side-by-side UI branches (`A`, `B`, `C`) on your phone without port forwarding, public tunnels, or staging deploys. Requires Tailscale and Python 3.9+ on Linux or macOS; no [`tailhome`](../../README.md) server is needed.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="../../docs/agents-dark.png">
  <img alt="An agent publishes three landing-page versions; tailhome on a phone groups them as Landing redesign with badges A, B and C" src="../../docs/agents.png">
</picture>

## Install

> [!TIP]
> Paste this into Claude Code, Codex, Gemini CLI, OpenCode or omp:
>
> ```text
> Install the tailnet-preview skill (npx skills add mickey/tailhome --skill tailnet-preview -g), then show me this app on my phone.
> ```

Or install it from your terminal on the machine where the app and agent run:

**Universal (`npx skills`, requires Node 22.20+):**

```sh
npx skills add mickey/tailhome --skill tailnet-preview -g
```

**Native agent plugins (no Node required):**

- **Claude Code:** `/plugin marketplace add mickey/tailhome` then `/plugin install tailnet-preview@tailhome`
- **omp:** `omp plugin marketplace add mickey/tailhome && omp plugin install tailnet-preview@tailhome`
- **Gemini CLI:** `gemini extensions install https://github.com/mickey/tailhome --ref main`
- **Manual copy:** Copy `skills/tailnet-preview/` into your agent's skills folder ([paths](references/serve.md#installation-and-updates)).

Reload or restart your agent after installing.

## Usage

Ask your agent in plain language:

- *"Can I see this on my phone?"*
- *"Publish three versions of the pricing hero so I can compare them."*
- *"Remove the landing previews."*

On first run, the agent checks your Tailscale setup and asks which HTTPS ports it may use (suggesting `8443-8452`), leaving your existing Serve mappings untouched. To pre-approve ports without being asked each time, set `TAILNET_PREVIEW_PORTS=8443-8452` in the agent's launch environment.

### Make previews automatic

If you work on a remote devbox or compare UI versions often, add this to your user-level `AGENTS.md` or `CLAUDE.md` (with your own port range):

```text
When you start something for me to look at (an app, a page, several versions),
publish it with the tailnet-preview skill on HTTPS ports 8443-8452 and give me
the link. Remove it when I say I'm done.
```

Adding this rule counts as your approval of those ports.

## Use with tailhome (optional)

If you also run [`tailhome`](../../README.md), it automatically groups your agent's previews at the top of `http://home/` with `A`, `B`, `C` comparison badges. Enable [preview discovery](../../docs/setup.md#optional-list-previews-in-tailhome) on `tailhome`:

| Variable | Set on | Purpose |
|---|---|---|
| `TAILNET_PREVIEW_PORTS` | Agent's environment | Pre-approved HTTPS ports for publishing; otherwise the agent asks |
| `TAILHOME_PREVIEW_PORTS` | `tailhome` | Extra HTTPS ports for `tailhome` to scan (does not approve publishing) |
| `TAILHOME_URL` | Agent's environment | Optional `https://home.<tailnet>.ts.net/` URL so the agent refreshes and verifies the listing |

## Requirements

On the publishing machine (checked automatically by the skill's `preflight` step on first run):

- Linux or macOS with Python 3.9+ and the Tailscale CLI connected.
- MagicDNS and HTTPS Certificates enabled in your [Tailscale DNS settings](https://console.tailscale.com/admin/dns).
- Permission to configure Serve (on Linux, run `sudo tailscale set --operator=$USER` once; see [permission errors](references/serve.md#read-preflight-results)).

On your phone or reviewer's device (not checked by host `preflight`):

- Tailscale connected on the viewing device, with access to the publishing machine and HTTPS port allowed by your [tailnet policy](https://tailscale.com/docs/features/access-control). Keep the publishing machine awake and the dev server running.

## Privacy and cleanup

- The skill runs locally on your machine and sends nothing to its author.
- If you use `tailhome`, everyone who can open `home/` sees listed preview titles, descriptions, addresses and status, even if they cannot open the app itself. Keep secrets and internal paths out of titles and descriptions.
- Browsers send a hostname's cookies across all its ports, including `HttpOnly` cookies. Only publish trusted app code on a machine where you stay signed in to other apps.
- Previews stay live until removed. Ask *"remove the landing previews"* when finished; cleanup removes only unchanged mappings recorded by the skill and leaves your apps, worktrees and packages intact.

Read the [privacy statement](PRIVACY.md), [agent instructions](SKILL.md) or [full reference](references/serve.md). [Source and issues](https://github.com/mickey/tailhome). [MIT license](../../LICENSE).
