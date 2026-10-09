package pub

import (
	"strings"
	"testing"
	"time"

	"whereis.cam/internal/homeassistant"
)

func sampleStates(camState string, camInZones []string) []homeassistant.EntityState {
	inZones := make([]interface{}, len(camInZones))
	for i, z := range camInZones {
		inZones[i] = z
	}

	return []homeassistant.EntityState{
		{
			EntityID: "person.cam",
			State:    camState,
			Attributes: map[string]interface{}{
				"friendly_name": "cam",
				"in_zones":      inZones,
				"latitude":      -37.7698,
				"longitude":     144.9458,
			},
			LastChanged: time.Now().Add(-45 * time.Minute),
		},
		{
			EntityID: "zone.home",
			State:    "1",
			Attributes: map[string]interface{}{
				"friendly_name": "Fitz",
				"icon":          "mdi:home",
				"latitude":      -37.7698,
				"longitude":     144.9458,
				"radius":        26.0,
			},
		},
		{
			EntityID: "zone.brunswick",
			State:    "1",
			Attributes: map[string]interface{}{
				"friendly_name": "Brunswick",
				"icon":          "mdi:home-floor-b",
				"latitude":      -37.7661,
				"longitude":     144.9541,
				"radius":        1809.0,
			},
		},
		{
			EntityID: "zone.union_square",
			State:    "0",
			Attributes: map[string]interface{}{
				"friendly_name": "Union Square",
				"icon":          "mdi:cart",
				"latitude":      -37.7721,
				"longitude":     144.9488,
				"radius":        68.0,
			},
		},
		{
			EntityID: "zone.the_vic",
			State:    "0",
			Attributes: map[string]interface{}{
				"friendly_name": "The Vic",
				"icon":          "mdi:glass-mug-variant",
				"latitude":      -37.7668,
				"longitude":     144.9589,
				"radius":        25.0,
			},
		},
		{
			EntityID: "zone.foreigner",
			State:    "0",
			Attributes: map[string]interface{}{
				"friendly_name": "Foreigner",
				"icon":          "mdi:glass-mug-variant",
				"latitude":      -37.7642,
				"longitude":     144.9545,
				"radius":        53.0,
			},
		},
	}
}

func TestDetectorNotAtPub(t *testing.T) {
	detector := NewDetector("person.cam", nil, nil)
	states := sampleStates("home", []string{"zone.home", "zone.brunswick"})

	status := detector.Detect(states)

	if status.AtPub {
		t.Errorf("expected AtPub false, got true")
	}
	if status.Headline != "Cam's not at a pub. Weird." {
		t.Errorf("expected headline \"Cam's not at a pub. Weird.\", got %q", status.Headline)
	}
	if status.Pub != nil {
		t.Errorf("expected Pub nil, got %v", status.Pub)
	}
	if status.Directions != nil {
		t.Errorf("expected Directions nil, got %v", status.Directions)
	}
	// Verify dynamically discovered tracked pubs
	if len(status.TrackedPubs) != 2 {
		t.Fatalf("expected 2 tracked pubs (The Vic, Foreigner), got %d", len(status.TrackedPubs))
	}
	if status.TrackedPubs[0].Name != "Foreigner" || status.TrackedPubs[1].Name != "The Vic" {
		t.Errorf("expected sorted pubs, got %v, %v", status.TrackedPubs[0].Name, status.TrackedPubs[1].Name)
	}
}

func TestDetectorAtPubByState(t *testing.T) {
	detector := NewDetector("person.cam", nil, nil)
	states := sampleStates("The Vic", []string{"zone.the_vic", "zone.brunswick"})

	status := detector.Detect(states)

	if !status.AtPub {
		t.Fatalf("expected AtPub true, got false")
	}
	if status.Headline != "Cam is at the pub!" {
		t.Errorf("expected headline \"Cam is at the pub!\", got %q", status.Headline)
	}
	if status.Pub == nil || status.Pub.Name != "The Vic" {
		t.Fatalf("expected pub name 'The Vic', got %v", status.Pub)
	}
	if status.Directions == nil {
		t.Fatal("expected directions to be populated")
	}
	if !strings.Contains(status.Directions.OpenStreetMap, "openstreetmap.org") {
		t.Errorf("expected OSM link, got %q", status.Directions.OpenStreetMap)
	}
	if !strings.Contains(status.Directions.AppleMaps, "maps.apple.com") {
		t.Errorf("expected Apple Maps link, got %q", status.Directions.AppleMaps)
	}
	if status.Pub.Duration != "45m" {
		t.Errorf("expected duration '45m', got %q", status.Pub.Duration)
	}
}

func TestDetectorAtPubByInZones(t *testing.T) {
	detector := NewDetector("person.cam", nil, nil)
	states := sampleStates("Foreigner", []string{"zone.foreigner"})

	status := detector.Detect(states)

	if !status.AtPub {
		t.Fatalf("expected AtPub true, got false")
	}
	if status.Pub == nil || status.Pub.Name != "Foreigner" {
		t.Fatalf("expected pub name 'Foreigner', got %v", status.Pub)
	}
}

func TestDetectorDynamicNewlyAddedPubByBeerIcon(t *testing.T) {
	detector := NewDetector("person.cam", nil, nil)
	states := sampleStates("home", []string{"zone.home"})

	// Cam adds a brand new brewery zone with mdi:beer icon in HA
	states = append(states, homeassistant.EntityState{
		EntityID: "zone.moon_dog",
		State:    "0",
		Attributes: map[string]interface{}{
			"friendly_name": "Moon Dog World",
			"icon":          "mdi:beer",
			"latitude":      -37.8100,
			"longitude":     144.9900,
			"radius":        30.0,
		},
	})

	status := detector.Detect(states)
	// Now 3 pubs should be automatically discovered
	if len(status.TrackedPubs) != 3 {
		t.Fatalf("expected 3 tracked pubs, got %d", len(status.TrackedPubs))
	}

	// Cam moves to Moon Dog
	states[0].State = "Moon Dog World"
	states[0].Attributes["in_zones"] = []interface{}{"zone.moon_dog"}

	statusAtNewPub := detector.Detect(states)
	if !statusAtNewPub.AtPub {
		t.Fatalf("expected AtPub true for newly added pub")
	}
	if statusAtNewPub.Pub.Name != "Moon Dog World" {
		t.Errorf("expected pub name Moon Dog World, got %q", statusAtNewPub.Pub.Name)
	}
}

func TestDetectorDynamicNewlyAddedPubByName(t *testing.T) {
	detector := NewDetector("person.cam", nil, nil)
	states := sampleStates("home", []string{"zone.home"})

	// Cam adds a pub named "The Retreat Hotel" with default or generic icon
	states = append(states, homeassistant.EntityState{
		EntityID: "zone.the_retreat",
		State:    "0",
		Attributes: map[string]interface{}{
			"friendly_name": "The Retreat Hotel",
			"icon":          "mdi:map-marker",
			"latitude":      -37.7700,
			"longitude":     144.9600,
			"radius":        25.0,
		},
	})

	status := detector.Detect(states)
	found := false
	for _, p := range status.TrackedPubs {
		if p.Name == "The Retreat Hotel" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected 'The Retreat Hotel' to be dynamically discovered as pub by name")
	}
}

func TestDetectorShoppingCenterNotPub(t *testing.T) {
	detector := NewDetector("person.cam", nil, nil)
	states := sampleStates("Union Square", []string{"zone.union_square"})

	status := detector.Detect(states)

	if status.AtPub {
		t.Errorf("expected Shopping Center not to be considered a pub, got AtPub=true")
	}
}

func TestDirectionsBuild(t *testing.T) {
	dirs := BuildDirections("The Grandview", -37.7663, 144.9489)
	if dirs == nil {
		t.Fatal("expected non-nil DirectionsLinks")
	}
	if !strings.Contains(dirs.OpenStreetMap, "-37.7663") {
		t.Errorf("expected lat in OSM link, got %q", dirs.OpenStreetMap)
	}
	if !strings.Contains(dirs.AppleMaps, "The+Grandview") {
		t.Errorf("expected encoded name in Apple Maps link, got %q", dirs.AppleMaps)
	}
	if !strings.Contains(dirs.GoogleMaps, "-37.7663") {
		t.Errorf("expected lat in Google Maps link, got %q", dirs.GoogleMaps)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "just arrived"},
		{15 * time.Minute, "15m"},
		{60 * time.Minute, "1h"},
		{90 * time.Minute, "1h 30m"},
	}

	for _, tc := range cases {
		got := formatDuration(tc.d)
		if got != tc.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestDetectorWalkByFilteredOut(t *testing.T) {
	detector := NewDetector("person.cam", nil, nil)
	detector.SetMinDwellTime(3 * time.Minute)

	// Cam just stepped into zone 45 seconds ago (walking by)
	states := sampleStates("The Vic", []string{"zone.the_vic"})
	states[0].LastChanged = time.Now().Add(-45 * time.Second)

	status := detector.Detect(states)

	// Should NOT declare AtPub yet!
	if status.AtPub {
		t.Errorf("expected AtPub false for 45s walk-by, got true")
	}
	if !status.SettlingIn {
		t.Errorf("expected SettlingIn true for 45s walk-by, got false")
	}
	if !strings.Contains(status.Headline, "Spotted near") {
		t.Errorf("expected headline to say Spotted near, got %q", status.Headline)
	}

	// Now fast-forward to 4 minutes (confirmed pint)
	states[0].LastChanged = time.Now().Add(-4 * time.Minute)
	confirmedStatus := detector.Detect(states)

	if !confirmedStatus.AtPub {
		t.Errorf("expected AtPub true after 4 minutes dwell time, got false")
	}
	if confirmedStatus.SettlingIn {
		t.Errorf("expected SettlingIn false after 4 minutes dwell time, got true")
	}
	if confirmedStatus.Headline != "Cam is at the pub!" {
		t.Errorf("expected confirmed headline, got %q", confirmedStatus.Headline)
	}
}
