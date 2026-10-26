# 🍺 whereis.cam

Find out which pub Cam is at in real time, powered by Home Assistant.

If Cam is at a pub, the website announces the venue, physical street address, time of first pour, and provides an instant app chooser for turn-by-turn directions. If he's not at a pub, it lets mates know: **"Cam's not at a pub. Weird."**

Styled with an authentic **70s Art Deco Saloon Bar** aesthetic — heavy timber bullnose plaque framing, nicotine cream enamel, burnt orange vinyl, mustard yellow coasters, slotted brass corner rivets, and 100% emoji-free custom retro vector iconography.

---

## Features

- 📍 **Home Assistant Integration**: Connects directly to a Home Assistant instance via REST API with in-memory TTL caching.
- 🍻 **Zero-Config Dynamic Pub Discovery**: Discovers drinking zones dynamically in Home Assistant via drink/glass icons, pub/brewery keywords, and zone radii.
- 🏛️ **1973 Art Deco Saloon Aesthetic**:
  - Handcrafted slotted brass corner screws, gold pinstripes, and vintage cardboard beer coaster frames.
  - **100% Emoji-Free**: Every icon is a custom retro SVG.
  - **Embedded SVG Sprite Sheet**: All vector icons are inlined directly in the HTML payload — **0 HTTP network hops, 0ms pop-in delay**.
  - **Self-Hosted Typography**: Zero third-party font calls (`Calistoga` and `Josefin Sans` served locally as WOFF2).
  - **WCAG AA Compliant**: All text and button contrasts exceed 4.5:1.
- 🗺️ **Physical Street Addresses & Universal App Chooser**:
  - Displays real physical street addresses for venues.
  - Tapping any address opens a universal action modal allowing users to choose their preferred navigation app:
    - 🍏 **Apple Maps** (native deep link)
    - 🗺️ **Google Maps** (turn-by-turn routing)
    - 🚇 **Citymapper** (transit routing)
    - 📋 **Copy Address** (1-tap clipboard copy for rideshares or messages)
- 🔔 **Direct Browser & Phone Notifications (PWA)**:
  - Standard **Web Push Protocol (RFC 8291/8292)** with server-side **VAPID ECDSA encryption**.
  - Installable as a standalone PWA on iOS (Add to Home Screen) and Android.
  - Background Service Worker (`/sw.js`) presents lock-screen notifications even when the browser tab is closed.
  - 1-tap activation right from the site.
- ✉️ **Real-Time Email Alerts (Resend API)**:
  - Friends can subscribe with their email to get notified when Cam hits the pub.
  - Welcome confirmation email sent instantly upon signup.
  - **Absolute Clock-Time Arrivals**: Displays exact Melbourne time (`🕒 First pot poured at 6:42 PM AEDT`) so timestamps never become stale in inboxes.
  - **Single-Click Unsubscribe**: Fully tokenized 1-click unsubscribe links and native RFC `List-Unsubscribe` headers.
  - **3-Hour Anti-Spam Debounce Cooldown**: Prevents alert fatigue per pub visit.
- ⏱️ **Background Autonomous Poller**:
  - Server-side 30s background ticker polls Home Assistant transitions independently of web traffic.
  - Strict alert isolation: `?mock=pub` preview requests are 100% isolated and never trigger subscriber alerts.

---

## Project Structure

```text
whereiscam/
├── cmd/
│   └── api/
│       ├── main.go               # Server entry point, background poller
│       └── main_test.go          # End-to-end integration tests
├── internal/
│   ├── alerts/                   # Email alerts (Resend API) & Web Push (VAPID)
│   │   ├── alerts.go             # Subscriber manager, templates, cooldown
│   │   ├── alerts_test.go
│   │   ├── push.go               # VAPID keygen, push subscriptions, broadcast
│   │   └── push_test.go
│   ├── config/                   # Configuration loader (.env & env vars)
│   ├── homeassistant/            # HA REST client with in-memory caching
│   ├── pub/                      # Pub detector, address resolver, directions
│   ├── handlers/                 # Web, status API, alerts, push, sw.js
│   └── middleware/               # Logging, panic recovery, security headers
├── web/
│   ├── embed.go                  # embed.FS for templates, static assets, fonts
│   ├── templates/
│   │   └── index.html            # 1973 Saloon plaque with embedded SVG sprite
│   └── static/
│       ├── fonts/                # Self-hosted Calistoga & Josefin Sans WOFF2
│       ├── manifest.json         # Standalone PWA web manifest
│       ├── sw.js                 # Service Worker for push & offline caching
│       └── style.css             # Responsive Saloon Bar CSS with brass/timber theme
├── data/                         # SQLite database (whereiscam.db) & backups (.gitignored)
├── Makefile
└── README.md
```

---

## API & Endpoints

| Endpoint | Method | Description |
| :--- | :--- | :--- |
| `/` | `GET` | Responsive 1973 Art Deco saloon web interface |
| `/api/status` | `GET` | Live JSON status (`?fresh=1` bypasses cache) |
| `/api/alerts/subscribe` | `POST` | Subscribe email for pub arrival alerts |
| `/unsubscribe?token=...` | `GET` | 1-click tokenized unsubscription page |
| `/api/push/vapid-public-key`| `GET` | Fetch VAPID public key for browser push |
| `/api/push/subscribe` | `POST` | Register browser push subscription endpoint |
| `/api/push/unsubscribe` | `POST` | Deactivate browser push subscription endpoint |
| `/sw.js` | `GET` | PWA Service Worker script |
| `/manifest.json` | `GET` | Web App Manifest |
| `/health` | `GET` | Health check (`{"status":"ok"}`) |

---

## Configuration

Set your configuration in `.env` (or via environment variables):

```env
# Home Assistant REST API
HASS_URL=https://homeAssistant.example.com
HASS_TOKEN=your_long_lived_access_token_here
PERSON_ENTITY=person.name

# HTTP Server
PORT=8080
CACHE_TTL_SECONDS=15

# Storage (SQLite)
DB_PATH=data/whereiscam.db

# Email Alerts (Resend)
RESEND_API_KEY=re_...
RESEND_FROM=alerts@whereis.cam
BASE_URL=https://whereis.cam

```

---

## Development & Usage

### Run Tests
```bash
make test
```

### Build & Run Locally
```bash
make build
./bin/whereiscam
```

### Visual Regression Tests
```bash
npm --prefix /Users/cam/stewart/ui-tests test
```
