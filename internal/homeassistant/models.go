package homeassistant

import (
	"strings"
	"time"
)

// EntityState represents an entity returned by the Home Assistant REST API.
type EntityState struct {
	EntityID    string                 `json:"entity_id"`
	State       string                 `json:"state"`
	Attributes  map[string]interface{} `json:"attributes"`
	LastChanged time.Time              `json:"last_changed"`
	LastUpdated time.Time              `json:"last_updated"`
}

// FriendlyName returns the friendly name attribute, or falls back to EntityID.
func (e EntityState) FriendlyName() string {
	if val, ok := e.Attributes["friendly_name"].(string); ok && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return e.EntityID
}

// Latitude extracts latitude as float64 if present.
func (e EntityState) Latitude() (float64, bool) {
	if val, ok := e.Attributes["latitude"].(float64); ok {
		return val, true
	}
	return 0, false
}

// Longitude extracts longitude as float64 if present.
func (e EntityState) Longitude() (float64, bool) {
	if val, ok := e.Attributes["longitude"].(float64); ok {
		return val, true
	}
	return 0, false
}

// Radius extracts zone radius in meters as float64 if present.
func (e EntityState) Radius() (float64, bool) {
	if val, ok := e.Attributes["radius"].(float64); ok {
		return val, true
	}
	return 0, false
}

// Icon extracts the icon attribute string.
func (e EntityState) Icon() string {
	if val, ok := e.Attributes["icon"].(string); ok {
		return strings.TrimSpace(val)
	}
	return ""
}

// InZones extracts the list of zone entity IDs the entity currently is inside.
func (e EntityState) InZones() []string {
	raw, ok := e.Attributes["in_zones"].([]interface{})
	if !ok {
		return nil
	}
	zones := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok && s != "" {
			zones = append(zones, s)
		}
	}
	return zones
}

// Persons extracts the list of person entity IDs assigned or inside this zone.
func (e EntityState) Persons() []string {
	raw, ok := e.Attributes["persons"].([]interface{})
	if !ok {
		return nil
	}
	persons := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok && s != "" {
			persons = append(persons, s)
		}
	}
	return persons
}

// HasPerson checks whether a given person entity ID is in this entity's persons list.
func (e EntityState) HasPerson(personID string) bool {
	for _, p := range e.Persons() {
		if strings.EqualFold(p, personID) {
			return true
		}
	}
	return false
}

// IsInZone checks whether a given zone entity ID is in this entity's in_zones list.
func (e EntityState) IsInZone(zoneID string) bool {
	for _, z := range e.InZones() {
		if strings.EqualFold(z, zoneID) {
			return true
		}
	}
	return false
}
