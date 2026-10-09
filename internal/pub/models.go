package pub

import (
	"time"
)

// DirectionsLinks provides URLs to open directions across mapping providers.
type DirectionsLinks struct {
	Primary       string `json:"primary"`
	OpenStreetMap string `json:"open_street_map"`
	AppleMaps     string `json:"apple_maps"`
	GoogleMaps    string `json:"google_maps"`
	Citymapper    string `json:"citymapper"`
}

// PubSummary provides a brief summary of a known pub zone.
type PubSummary struct {
	Name      string  `json:"name"`
	EntityID  string  `json:"entity_id"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Address   string  `json:"address"`
	MapURL    string  `json:"map_url,omitempty"`
}

// Pub contains details about the pub Cam is currently visiting.
type Pub struct {
	Name      string    `json:"name"`
	EntityID  string    `json:"entity_id"`
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Address   string    `json:"address"`
	Icon      string    `json:"icon"`
	Since     time.Time `json:"since"`
	Duration  string    `json:"duration"`
	ArrivedAt string    `json:"arrived_at"`
}

// Status represents the evaluated status of whether Cam is at a pub.
type Status struct {
	AtPub       bool             `json:"at_pub"`
	Pub         *Pub             `json:"pub,omitempty"`
	Headline    string           `json:"headline"`
	Subtext     string           `json:"subtext"`
	Directions  *DirectionsLinks `json:"directions,omitempty"`
	TrackedPubs []PubSummary     `json:"tracked_pubs,omitempty"`
	CheckedAt   time.Time        `json:"checked_at"`
}
