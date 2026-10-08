package pub

import (
	"fmt"
	"net/url"
)

// BuildDirections creates direction and navigation links across multiple map providers.
func BuildDirections(pubName string, lat, lon float64) *DirectionsLinks {
	escapedName := url.QueryEscape(pubName)

	// OpenStreetMap directions (Open-source, tracker-free default)
	osmURL := fmt.Sprintf("https://www.openstreetmap.org/directions?engine=fossgis_osrm_car&route=;%f%%2C%f", lat, lon)

	// Apple Maps directions
	appleURL := fmt.Sprintf("https://maps.apple.com/?daddr=%f,%f&q=%s", lat, lon, escapedName)

	// Google Maps directions
	googleURL := fmt.Sprintf("https://www.google.com/maps/dir/?api=1&destination=%f,%f", lat, lon)

	// Citymapper directions (popular for Melbourne / public transit)
	citymapperURL := fmt.Sprintf("https://citymapper.com/directions?endcoord=%f%%2C%f&endname=%s", lat, lon, escapedName)

	return &DirectionsLinks{
		Primary:       osmURL,
		OpenStreetMap: osmURL,
		AppleMaps:     appleURL,
		GoogleMaps:    googleURL,
		Citymapper:    citymapperURL,
	}
}
