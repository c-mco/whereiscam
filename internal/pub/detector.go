package pub

import (
	"fmt"
	"math/rand"
	"net/url"
	"sort"
	"strings"
	"time"

	"whereis.cam/internal/homeassistant"
)

// Detector determines whether the tracked person is currently at a pub zone.
type Detector struct {
	personEntity string
	pubZones     map[string]bool
	iconKeywords []string
	nameKeywords []string
	minDwellTime time.Duration
	random       *rand.Rand
}

// NewDetector initializes a detector with configuration rules.
func NewDetector(personEntity string, explicitPubZones []string, iconKeywords []string) *Detector {
	zoneMap := make(map[string]bool, len(explicitPubZones))
	for _, z := range explicitPubZones {
		clean := strings.ToLower(strings.TrimSpace(z))
		if clean != "" {
			zoneMap[clean] = true
		}
	}

	keywords := make([]string, 0, len(iconKeywords))
	for _, k := range iconKeywords {
		clean := strings.ToLower(strings.TrimSpace(k))
		if clean != "" {
			keywords = append(keywords, clean)
		}
	}
	if len(keywords) == 0 {
		keywords = []string{
			"glass", "beer", "mug", "wine", "pub", "bar",
			"cocktail", "liquor", "bottle", "brewery", "tap",
			"pint", "saloon", "tavern", "alcohol", "beverage",
			"cheers", "whiskey", "spirits",
		}
	}

	nameKeywords := []string{
		"pub", "brewery", "brewing", "taphouse", "taproom",
		"tavern", "alehouse", "distillery", "saloon", "hotel",
	}

	return &Detector{
		personEntity: strings.ToLower(strings.TrimSpace(personEntity)),
		pubZones:     zoneMap,
		iconKeywords: keywords,
		nameKeywords: nameKeywords,
		minDwellTime: 5 * time.Minute,
		random:       rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Detect analyzes states and returns the current Pub status.
func (d *Detector) Detect(states []homeassistant.EntityState) Status {
	now := time.Now()

	var person *homeassistant.EntityState
	zonesByID := make(map[string]homeassistant.EntityState)

	for i := range states {
		st := &states[i]
		if strings.EqualFold(st.EntityID, d.personEntity) {
			person = st
		} else if strings.HasPrefix(strings.ToLower(st.EntityID), "zone.") {
			zonesByID[strings.ToLower(st.EntityID)] = *st
		}
	}

	// 1. Dynamically identify all pub zones in Home Assistant
	pubZones := make(map[string]homeassistant.EntityState)
	for id, z := range zonesByID {
		if d.isPubZone(z) {
			pubZones[id] = z
		}
	}

	// Build sorted summary of all tracked pubs
	trackedPubs := make([]PubSummary, 0, len(pubZones))
	for _, z := range pubZones {
		lat, _ := z.Latitude()
		lon, _ := z.Longitude()
		name := z.FriendlyName()
		mapURL := fmt.Sprintf("https://www.google.com/maps/search/?api=1&query=%s+Brunswick", url.QueryEscape(name))
		if lat != 0 && lon != 0 {
			mapURL = fmt.Sprintf("https://www.google.com/maps/search/?api=1&query=%f,%f", lat, lon)
		}
		trackedPubs = append(trackedPubs, PubSummary{
			Name:      name,
			EntityID:  z.EntityID,
			Latitude:  lat,
			Longitude: lon,
			Address:   ResolvePubAddress(name),
			MapURL:    mapURL,
		})
	}
	sort.Slice(trackedPubs, func(i, j int) bool {
		return strings.ToLower(trackedPubs[i].Name) < strings.ToLower(trackedPubs[j].Name)
	})

	if person == nil {
		return Status{
			AtPub:       false,
			Headline:    "Cam's not at a pub. Weird.",
			Subtext:     "Tracker signal not found. He might be off the grid.",
			TrackedPubs: trackedPubs,
			CheckedAt:   now,
		}
	}

	// 2. Check if person is at any pub zone
	for _, pubZone := range pubZones {
		if d.personAtZone(*person, pubZone) {
			pubName := pubZone.FriendlyName()
			lat, _ := pubZone.Latitude()
			lon, _ := pubZone.Longitude()

			// Fallback to person GPS if zone lat/long is zero or missing
			if lat == 0 && lon == 0 {
				lat, _ = person.Latitude()
				lon, _ = person.Longitude()
			}

			since := person.LastChanged
			if since.IsZero() {
				since = now
			}
			dwell := now.Sub(since)
			if dwell < 0 {
				dwell = 0
			}

			// Walk-by filter: if dwell time has not reached the threshold, Cam is just walking past
			if d.minDwellTime > 0 && dwell < d.minDwellTime {
				continue
			}

			duration := formatDuration(dwell)
			directions := BuildDirections(pubName, lat, lon)

			// Confirmed pub visit (dwell time >= minDwellTime)
			return Status{
				AtPub: true,
				Pub: &Pub{
					Name:      pubName,
					EntityID:  pubZone.EntityID,
					Latitude:  lat,
					Longitude: lon,
					Address:   ResolvePubAddress(pubName),
					Icon:      pubZone.Icon(),
					Since:     since,
					Duration:  duration,
					ArrivedAt: formatClockTime(since),
				},
				Headline:    "Cam is at the pub!",
				Subtext:     fmt.Sprintf("Pints are pouring at %s. Pull up a stool and join him!", pubName),
				Directions:  directions,
				TrackedPubs: trackedPubs,
				CheckedAt:   now,
			}
		}
	}

	// Not at a pub
	return Status{
		AtPub:       false,
		Headline:    "Cam's not at a pub. Weird.",
		Subtext:     d.dynamicNotAtPubSubtext(trackedPubs),
		TrackedPubs: trackedPubs,
		CheckedAt:   now,
	}
}

// isPubZone evaluates whether a zone qualifies as a pub dynamically.
func (d *Detector) isPubZone(zone homeassistant.EntityState) bool {
	zid := strings.ToLower(zone.EntityID)
	zname := strings.ToLower(zone.FriendlyName())

	// 1. Explicit list override always wins
	if d.pubZones[zid] || d.pubZones[zname] {
		return true
	}

	// 2. Exclude obvious non-pubs:
	// Exclude home zone
	if zid == "zone.home" || zname == "home" || strings.Contains(zname, "home") {
		return false
	}
	icon := strings.ToLower(zone.Icon())
	if icon == "mdi:home" || strings.HasPrefix(icon, "mdi:home-") {
		return false
	}

	// Exclude non-pub facilities (supermarkets, shopping, schools, transport, offices)
	nonPubIcons := []string{
		"cart", "shopping", "store", "school", "hospital",
		"airport", "train", "bus", "office", "briefcase", "gym", "doctor", "bank",
	}
	for _, npi := range nonPubIcons {
		if strings.Contains(icon, npi) {
			return false
		}
	}

	// Exclude broad suburb / municipality zones (pubs rarely exceed 500m radius)
	if radius, ok := zone.Radius(); ok && radius > 500 {
		return false
	}

	// 3. Match by beverage / pub icons
	for _, kw := range d.iconKeywords {
		if strings.Contains(icon, kw) {
			return true
		}
	}

	// 4. Match by zone entity name or ID keywords (e.g. zone.pub_something, The Retreat Hotel, etc.)
	if strings.HasPrefix(zid, "zone.pub_") || strings.HasSuffix(zid, "_pub") {
		return true
	}
	for _, nkw := range d.nameKeywords {
		if containsWord(zname, nkw) || containsWord(strings.TrimPrefix(zid, "zone."), nkw) {
			return true
		}
	}

	// Also check for "bar" explicitly
	if containsWord(zname, "bar") || strings.Contains(zid, "_bar") || strings.Contains(zid, "bar_") {
		return true
	}

	return false
}

// containsWord checks if word is present as a standalone token or part of name.
func containsWord(s, word string) bool {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '-' || r == '_' || r == '.'
	})
	for _, f := range fields {
		if strings.EqualFold(f, word) {
			return true
		}
	}
	return false
}

// personAtZone checks if the person is inside the given zone.
func (d *Detector) personAtZone(person, zone homeassistant.EntityState) bool {
	zid := strings.ToLower(zone.EntityID)
	zname := strings.ToLower(zone.FriendlyName())
	pState := strings.ToLower(strings.TrimSpace(person.State))

	// Direct state match (HA sets person state to zone friendly name)
	if pState == zname || pState == zid || pState == strings.TrimPrefix(zid, "zone.") {
		return true
	}

	// InZones attribute check
	if person.IsInZone(zone.EntityID) || person.IsInZone(zid) {
		return true
	}

	// Zone's persons list check
	if zone.HasPerson(person.EntityID) || zone.HasPerson(d.personEntity) {
		return true
	}

	return false
}

func (d *Detector) dynamicNotAtPubSubtext(trackedPubs []PubSummary) string {
	quips := []string{
		"Cam frequents plenty of Brunswick locals, but he is off-duty right now.",
		"Checked his regular locals: all clear. Check back around happy hour to join him.",
		"Currently elsewhere in Brunswick. Keep an eye on the radar if you want to catch him for a pint.",
		"Reports indicate sobriety. Highly irregular for Cam.",
		"Must be between rounds. Check back soon if you fancy a drink with Cam.",
		"Check back around happy hour to see which pub he lands at.",
		"System diagnostics: Pint levels critically low. He is bound to drop in somewhere soon.",
	}

	count := len(trackedPubs)
	if count > 0 {
		quips = append(quips,
			fmt.Sprintf("Scanning %d tracked locals in real time.", count),
			fmt.Sprintf("Taps are pouring at %d locals, but Cam is MIA.", count),
		)
		if count >= 2 {
			p1 := trackedPubs[0].Name
			p2 := trackedPubs[d.random.Intn(count-1)+1].Name
			quips = append(quips,
				fmt.Sprintf("Not at %s, not at %s. Where could he be?", p1, p2),
			)
		}
	}

	idx := d.random.Intn(len(quips))
	return quips[idx]
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return "just arrived"
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		return fmt.Sprintf("%dm", mins)
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	if mins == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh %dm", hours, mins)
}

var melbourneLoc *time.Location

func init() {
	var err error
	melbourneLoc, err = time.LoadLocation("Australia/Melbourne")
	if err != nil {
		melbourneLoc = time.FixedZone("AEDT", 11*60*60)
	}
}

func formatClockTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(melbourneLoc).Format("3:04 PM MST")
}

// ResolvePubAddress returns the street address for known Brunswick pubs or a sensible fallback.
func ResolvePubAddress(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.Contains(lower, "grandview"):
		return "47 Pearson St, Brunswick West"
	case strings.Contains(lower, "coconspirators"):
		return "377 Victoria St, Brunswick"
	case strings.Contains(lower, "foreigner"):
		return "102-104 Victoria St, Brunswick"
	case strings.Contains(lower, "the union") || lower == "union":
		return "109 Union St, Brunswick"
	case strings.Contains(lower, "the vic") || strings.Contains(lower, "victoria hotel"):
		return "280 Victoria St, Brunswick"
	default:
		return name + ", Brunswick VIC"
	}
}

// SetMinDwellTime configures the minimum duration the person must reside in a pub zone
// before confirming they are at the pub (filtering out walk-bys).
func (d *Detector) SetMinDwellTime(dur time.Duration) {
	d.minDwellTime = dur
}
