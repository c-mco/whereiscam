"""Minimal fake Home Assistant for running whereis.cam locally.

Usage: python3 -I mockha.py <port> [cam_zone]
  cam_zone  optional zone entity Cam is in, e.g. zone.the_vic (default: home).
            He is reported as having arrived 20 minutes ago, past the 5 minute
            dwell filter, so the site shows him at the pub.
"""
import json
import sys
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer

ZONES = [
    ("zone.the_vic", "The Vic", -37.766832, 144.958952),
    ("zone.the_grandview", "The Grandview", -37.766332, 144.948903),
    ("zone.foreigner", "Foreigner", -37.764280, 144.954560),
    ("zone.the_union", "The Union", -37.773434, 144.953131),
]

port = int(sys.argv[1])
cam_zone = sys.argv[2] if len(sys.argv) > 2 else ""


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        if self.path.startswith("/api/states"):
            arrived = (datetime.now(timezone.utc) - timedelta(minutes=20)).isoformat()
            names = {z[0]: z[1] for z in ZONES}
            states = [{
                "entity_id": "person.cam",
                "state": names.get(cam_zone, "home"),
                "attributes": {
                    "friendly_name": "cam",
                    "in_zones": [cam_zone] if cam_zone else ["zone.home"],
                    "latitude": -37.77,
                    "longitude": 144.95,
                },
                "last_changed": arrived,
            }]
            for entity, name, lat, lon in ZONES:
                states.append({
                    "entity_id": entity,
                    "state": "1" if entity == cam_zone else "0",
                    "attributes": {"friendly_name": name, "icon": "mdi:glass-mug-variant", "latitude": lat, "longitude": lon},
                    "last_changed": arrived,
                })
            body = json.dumps(states).encode()
        elif self.path.rstrip("/") == "/api":
            body = b'{"message":"API running."}'
        else:
            self.send_response(404)
            self.end_headers()
            return
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(body)


HTTPServer(("127.0.0.1", port), Handler).serve_forever()
