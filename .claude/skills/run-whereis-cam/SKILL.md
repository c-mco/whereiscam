---
name: run-whereis-cam
description: Build, run, and drive whereis.cam locally. Use when asked to start or run the whereis.cam server, take screenshots of the public page or dashboard, test/click through the UI at desktop or phone sizes, check popups/toasts/buttons, or run the Go tests.
---

whereis.cam is a Go server (`cmd/api`) that renders a public "is Cam at the pub" page and an admin dashboard from Home Assistant data. To run it locally, start it against a fake Home Assistant with `serve.sh`, then drive it in headless Chrome with `driver.mjs` (Playwright).

All paths are relative to the repo root. Skill files live in `.claude/skills/run-whereis-cam/`.

## Prerequisites

- Go (module targets go 1.26), Python 3, Node 20+, `curl`, `lsof`.
- Google Chrome, or Playwright's bundled browser (see Setup).

## Setup

```bash
npm install --prefix .claude/skills/run-whereis-cam
# Only needed if Google Chrome isn't installed:
.claude/skills/run-whereis-cam/node_modules/.bin/playwright install chromium-headless-shell
```

## Run (agent path)

```bash
.claude/skills/run-whereis-cam/serve.sh start                 # Cam at home
.claude/skills/run-whereis-cam/serve.sh start zone.the_vic    # Cam at The Vic (past the 5-min dwell filter)
node .claude/skills/run-whereis-cam/driver.mjs sweep          # click everything; exit 1 + list on problems
node .claude/skills/run-whereis-cam/driver.mjs shot / --vp phone --full
node .claude/skills/run-whereis-cam/driver.mjs shot /dashboard --login
.claude/skills/run-whereis-cam/serve.sh stop
```

`serve.sh start`:
- rebuilds the binary into `$TMPDIR/whereis-run/`
- starts the fake Home Assistant on :8123 (four Brunswick pub zones) and the app on :8099
- wipes the database and seeds three subscribers
- sets the dashboard password to `testpass`

Server log: `$TMPDIR/whereis-run/server.log`. Screenshots: `$TMPDIR/whereis-run/shots/`; the driver prints each path. Read the PNGs to actually look at them.

| driver command | what it does |
|---|---|
| `shot <path> [--vp desktop\|phone\|landscape\|se] [--login] [--full] [--out name]` | Screenshot one page. `--login` signs in to the dashboard first. |
| `sweep` | Public page (normal and `?mock=pub`) and dashboard at desktop and phone sizes, plus the directions sheet on landscape and iPhone SE. Opens every directions sheet and closes it via Cancel and the backdrop, subscribes, runs every override mode, sends a test email, deactivates a subscriber. Flags anything off-screen, unclickable, scrolling sideways, or throwing JS errors. |

To exercise one change, `/?mock=pub` renders the at-pub page without Home Assistant. Override endpoints also flip the public state (dashboard → Manually Set Pub).

## Test

```bash
go vet ./... && go test ./...
```

All packages pass.

## Deploy (production on this Mac)

Production runs natively under launchd (`me.whereis.cam`, plist in `~/Library/LaunchAgents/`), working dir = repo root, so it reads the repo's `.env` and `data/whereiscam.db`. It runs `bin/whereiscam` on :8092, logging to `logs/err.log`. nginx runs in Docker (container `nginx`, config in `~/stewart/config/nginx/conf.d/whereis.cam.conf`, mounted read-only). It proxies `whereis.cam` and the Authelia-gated `whereis.itspartyti.me` (dashboard) to `host.docker.internal:8092`; those requests reach the app from `127.0.0.1`.

```bash
# 1. back up first: startup runs DB migrations
BK=data/backup-$(date +%Y%m%d-%H%M%S); mkdir -p $BK
sqlite3 data/whereiscam.db ".backup '$BK/whereiscam.db'" && cp bin/whereiscam $BK/whereiscam.prev
# 2. build + restart
go build -o bin/whereiscam.new ./cmd/api && mv bin/whereiscam.new bin/whereiscam
launchctl kickstart -k gui/$(id -u)/me.whereis.cam
tail -5 logs/err.log        # expect "Server listening" + "Connected to Home Assistant"
# 3. if you changed nginx config:
docker exec nginx nginx -t && docker exec nginx nginx -s reload
# 4. read-only smoke test of the live site (never run `sweep` against prod: it writes data)
BASE_URL=https://whereis.cam node .claude/skills/run-whereis-cam/driver.mjs shot / --vp phone --full --out live_phone
```

Rollback: `cp $BK/whereiscam.prev bin/whereiscam && launchctl kickstart -k gui/$(id -u)/me.whereis.cam`.

Production `.env` must have `TRUSTED_PROXIES=127.0.0.1` and `BIND_ADDR=127.0.0.1`. Docker Desktop's `host.docker.internal` still reaches a loopback-only app (verified with `docker exec nginx wget -qO- http://host.docker.internal:8092/health`), while the LAN can't. The dashboard auth is Authelia-only: there's no `ADMIN_PASSWORD`, so the password form returns 403. In nginx, `proxy_set_header Remote-User` must be set **inside** each `location` after `include proxy_params`, because a server-level one isn't inherited. The public vhost must clear it (`proxy_set_header Remote-User "";`).

## Gotchas

- **Never run the binary from the repo root for testing.** The server loads `./.env` and the legacy `./data/*.json` files from its working directory, which pulls in the real Home Assistant token and subscribers. `serve.sh` runs it from `$TMPDIR/whereis-run` for that reason.
- **Web Push can't be tested headless.** Chrome logs "Push API in incognito mode" / "Registration failed - permission denied". The driver ignores that noise; test push on a real phone.
- **The "Sweep Taps" button only exists when Cam is *not* at a pub.** It's absent on `?mock=pub` and when started with a zone.
- **Most dashboard actions reload the page about 800ms after showing their toast.** Wait for `networkidle` before the next click or you'll click a detached node.
- **Service worker caching.** `web/static/sw.js` is network-first; bump `CACHE_NAME` if you change caching behaviour. A cache-first version once left returning visitors on stale CSS.
- **Remote-User / TRUSTED_PROXIES.** Locally nothing is trusted, so the dashboard needs the password. To test proxy auth, set `TRUSTED_PROXIES=127.0.0.1` and send `Remote-User`.

## Troubleshooting

- **`browserType.launch: Executable doesn't exist at .../chromium_headless_shell-1200/...`**: Chrome isn't installed (or `PW_CHANNEL` names a missing channel), and the bundled browser hasn't been downloaded. Run the `playwright install chromium-headless-shell` line from Setup.
- **`server failed to start`**: `serve.sh` tails `server.log`. A port still in use clears on `serve.sh stop`.
