# Contributing to whereis.cam

Welcome! This document covers everything you need to know to develop, test, and contribute to **whereis.cam**.

---

## 🧭 Core Philosophy & Rules

1. **Zero External Dependencies**
   - We use **pure Go standard library** (`net/http`, `html/template`, `embed`, `encoding/json`, `sync`, `time`, etc.).
   - Do **not** add third-party packages (no `gin`, `chi`, `godotenv`, `testify`, etc.) to `go.mod`. Standard library Go is fast, reliable, and keeps maintenance overhead near zero.
2. **Self-Contained Single Binary**
   - All HTML templates, CSS styles, and SVG assets are compiled directly into the binary using Go's `embed.FS` (`web/embed.go`).
   - The compiled binary has no external asset or filesystem dependencies at runtime.
3. **Privacy & Credential Safety**
   - Never commit or log Home Assistant tokens or credentials.
   - When Cam is *not* at a pub, raw home/device GPS coordinates are never exposed via the web UI or JSON API. Only the evaluated pub zone's venue coordinates are provided when Cam is at a pub.

---

## 🏗️ Architecture Tour

```text
cmd/api/
  ├── main.go             # Server wiring, signal traps for graceful shutdown
  └── main_test.go        # End-to-end integration test with mock Home Assistant

internal/
  ├── config/             # Reads .env (manual parser, no godotenv) and environment vars
  ├── homeassistant/      # REST API client (/api/states) with thread-safe in-memory TTL cache
  ├── pub/                # Pub detector, presence evaluator, and map directions generator
  ├── handlers/           # HTTP handlers using Go 1.22+ method routing (ServeMux)
  └── middleware/         # Standard library HTTP middlewares (logging, security headers, recovery)

web/
  ├── embed.go            # embed.FS export for templates and static assets
  ├── templates/          # HTML templates (index.html)
  └── static/             # CSS (style.css) and SVG favicon (favicon.svg)
```

### How Pub Detection Works
In `internal/pub/detector.go`:
1. **Dynamic Pub Zone Discovery**: Zones in Home Assistant are automatically discovered at runtime without restarting or reconfiguring:
   - **Icon match**: any beverage/bar icon (e.g. `mdi:glass-mug-variant`, `mdi:beer`, `mdi:pub`, `mdi:cocktail`, `mdi:wine-glass`, `mdi:bottle-wine`, `mdi:tap`, `mdi:liquor`).
   - **Name or ID keywords**: zone entity ID or friendly name contains keywords like `pub`, `bar`, `brewery`, `brewing`, `taphouse`, `taproom`, `tavern`, `hotel`, `arms`, `alehouse`, `distillery`.
   - **Automatic exclusions**: excludes home zones, non-pub venues (shopping malls with `mdi:cart`, transit, schools), and broad suburb zones with radius > 500m.
   - **Explicit override**: optional `PUB_ZONES` env var if you ever need manual pinning.
2. **Presence Verification**: Cam is considered at a pub if:
   - `person.cam`'s `state` matches the pub zone's friendly name (e.g. `"The Vic"`), OR
   - The pub's zone entity ID is inside `person.cam`'s `in_zones` attribute list, OR
   - `person.cam` is present in the pub zone's `persons` attribute list.
3. If detected, venue coordinates and duration are packaged with map direction URLs.
4. If not detected, status returns `AtPub = false`, displays `"Cam's not at a pub. Weird."`, dynamically renders the list of all currently tracked locals, and picks a dynamic quip referencing the actual venues.

---

## 🛠️ Local Development Setup

### 1. Prerequisites
- **Go 1.22+** (tested with Go 1.26 on macOS arm64)
- `make` (optional, but convenient)

### 2. Configure Environment
Copy `.env.example` to `.env`:
```bash
cp .env.example .env
```

Set your Home Assistant credentials:
```env
HASS_URL=https://HomeAssistant.example.com
HASS_TOKEN=your_token_here
PERSON_ENTITY=person.cam
PORT=8080
CACHE_TTL_SECONDS=15
```

> **Developing without a live Home Assistant?**  
> You can run unit and integration tests (`make test`) without live credentials, as tests use standard library `httptest.Server` mocks.

### 3. Run Development Server
```bash
make run
```
Visit `http://localhost:8080`.

---

## 🧪 Testing

We practice thorough automated testing using Go's built-in `testing` and `net/http/httptest` packages:

```bash
# Run all tests
make test

# Run all tests without cache
go test -count=1 -v ./...

# Run a specific package test
go test -v ./internal/pub
```

### Adding Tests
- Every new feature or bug fix **must** include accompanying unit tests.
- For HTTP client or handler tests, use `httptest.NewServer` or `httptest.NewRecorder`.

---

## 💡 How To Extend

### Adding a New Map Provider
1. In `internal/pub/models.go`, add the provider field to `DirectionsLinks`:
   ```go
   type DirectionsLinks struct {
       // ... existing fields ...
       Waze string `json:"waze"`
   }
   ```
2. In `internal/pub/directions.go`, format the URL in `BuildDirections()`:
   ```go
   wazeURL := fmt.Sprintf("https://waze.com/ul?ll=%f,%f&navigate=yes", lat, lon)
   ```
3. In `web/templates/index.html`, add a link or pill button in `.directions-grid`.
4. Update `internal/pub/detector_test.go` to assert the link is generated.

### Adding New "Not At A Pub" Quips
Open `internal/pub/detector.go` and add lines to the `quips` slice inside `randomNotAtPubSubtext()`:
```go
quips := []string{
    "Checked the taps: all dry. Try again later.",
    "Currently elsewhere in the universe. Probably Brunswick.",
    // Add your new cheeky quip here!
}
```

### Frontend Styling
- Styling is vanilla CSS located in `web/static/style.css`.
- Uses CSS custom properties (`--accent-amber`, `--bg-main`, etc.) for consistent cozy pub aesthetics.
- Responsive design is mobile-first. Test on small screens (down to 360px wide).

---

## 📋 Contribution Checklist

Before submitting a pull request:
- [ ] Code is formatted with `gofmt -s -w .`
- [ ] Code passes linting: `go vet ./...`
- [ ] All tests pass: `go test ./...`
- [ ] Zero external dependencies added to `go.mod`
- [ ] No secrets or personal credentials committed
