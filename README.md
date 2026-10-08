# 🍺 whereis.cam

Find out which pub Cam is at in real time, powered by Home Assistant.

If Cam is at a pub, the website announces the pub, how long he's been there, and provides one-click directions. If he's not at a pub, it lets friends know: **"Cam's not at a pub. Weird."**

Built with **pure Go standard library** — zero external third-party packages or runtime dependencies.

---

## Features

- 📍 **Home Assistant Integration**: Connects directly to `fitz.itspartyti.me` via REST API.
- 🍻 **Zero-Config Dynamic Pub Discovery**: Add any new pub zone in Home Assistant and it appears automatically! Discovers zones dynamically via:
  - Drinking/nightlife icons (`mdi:glass-mug-variant`, `mdi:beer`, `mdi:pub`, `mdi:glass-cocktail`, `mdi:wine-glass`, etc.)
  - Venue keywords in name or entity ID (`pub`, `bar`, `brewery`, `taphouse`, `tavern`, `hotel`, `arms`, `distillery`)
  - Auto-excludes home zones, shopping centers, and broad suburb boundaries (>500m radius)
  - Displays a live "Scanning X Local Pubs" roster when Cam is not at a pub.
- 🗺️ **Un-Googled Directions**: Features **OpenStreetMap** and **Apple Maps** prominently, alongside Citymapper and Google Maps.
- ⚡ **Zero External Dependencies**: Pure Go stdlib (`net/http`, `html/template`, `embed`, `encoding/json`, `time`).
- 📦 **Single Self-Contained Binary**: HTML templates, responsive CSS, and SVG icons are embedded directly using Go's `embed.FS`.
- ⏱️ **In-Memory Cache**: Built-in TTL caching (default 15s) so page visits don't spam Home Assistant.
- 📡 **Live Updates & JSON API**: Includes `/api/status` for iOS Shortcuts, scripts, or widgets, plus lightweight browser polling.

---

## Project Structure

```text
whereiscam/
├── cmd/
│   └── api/
│       ├── main.go               # Server entry point, graceful shutdown
│       └── main_test.go          # End-to-end integration tests
├── internal/
│   ├── config/                   # Configuration loader (.env & env vars)
│   ├── homeassistant/            # HA REST client with in-memory caching
│   ├── pub/                      # Pub detection logic & directions generator
│   ├── handlers/                 # HTTP handlers (web page, JSON API, health)
│   └── middleware/               # Logging, panic recovery, security headers
├── web/
│   ├── embed.go                  # embed.FS for templates and static assets
│   ├── templates/
│   │   └── index.html            # Responsive HTML template
│   └── static/
│       ├── favicon.svg           # Beer mug SVG icon
│       └── style.css             # Vanilla CSS with warm pub aesthetic
├── .env.example                  # Environment variable template
├── .gitignore
├── Makefile
└── README.md
```

---

## Configuration

Copy `.env.example` to `.env`:

```bash
cp .env.example .env
```

Set your configuration in `.env` (or via system environment variables):

```env
# Home Assistant REST API
HASS_URL=https://fitz.itspartyti.me
HASS_TOKEN=your_long_lived_access_token_here
PERSON_ENTITY=person.cam

# HTTP Server
PORT=8080
CACHE_TTL_SECONDS=15

# Optional: override / explicitly restrict pub zones (comma-separated entity IDs)
# PUB_ZONES=zone.the_vic,zone.foreigner,zone.the_grandview,zone.the_union
```

---

## Development & Usage

### Run locally
```bash
make run
```
The server will start at `http://localhost:8080`.

### Build standalone binary
```bash
make build
# Output placed at bin/whereiscam
./bin/whereiscam
```

### Run tests
```bash
make test
```

---

## 📚 Documentation & Guides

- [**Contributing & Developer Guide**](CONTRIBUTING.md): Code architecture, zero-dependency philosophy, test suite guidelines, and how to extend the pub detector or map providers.
- [**DNS & Domain Setup Guide**](DNS_SETUP.md): Configuring DNS records, Cloudflare Tunnel, Caddy/Nginx reverse proxy, and SSL/TLS certificates for `whereis.cam`.

---

## API Endpoints

### `GET /`
Returns the responsive web page showing whether Cam is at a pub.

### `GET /api/status`
Returns live JSON status:

```json
{
  "at_pub": true,
  "pub": {
    "name": "The Vic",
    "entity_id": "zone.the_vic",
    "latitude": -37.766832,
    "longitude": 144.958952,
    "icon": "mdi:glass-mug-variant",
    "since": "2026-10-08T18:30:00Z",
    "duration": "45m"
  },
  "headline": "Cam is at the pub!",
  "subtext": "Pints are flowing at The Vic.",
  "directions": {
    "primary": "https://www.openstreetmap.org/directions?engine=fossgis_osrm_car&route=;-37.766832%2C144.958952",
    "open_street_map": "https://www.openstreetmap.org/directions?engine=fossgis_osrm_car&route=;-37.766832%2C144.958952",
    "apple_maps": "https://maps.apple.com/?daddr=-37.766832,144.958952&q=The+Vic",
    "google_maps": "https://www.google.com/maps/dir/?api=1&destination=-37.766832,144.958952",
    "citymapper": "https://citymapper.com/directions?endcoord=-37.766832%2C144.958952&endname=The+Vic"
  },
  "checked_at": "2026-10-08T19:15:00Z"
}
```

When not at a pub:
```json
{
  "at_pub": false,
  "headline": "Cam's not at a pub. Weird.",
  "subtext": "Checked the taps: all dry. Try again later.",
  "checked_at": "2026-10-08T19:15:00Z"
}
```

Add `?fresh=1` to bypass the 15-second cache and force a live check against Home Assistant.

### `GET /health`
Returns `{"status":"ok"}` for health checks and load balancers.
