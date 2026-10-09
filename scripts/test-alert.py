#!/usr/bin/env python3
"""
scripts/test-alert.py - Developer test utility for whereis.cam alerts.
Safely tests email notifications by sending directly to an explicit address.
NEVER broadcasts to production subscribers.
"""

import argparse
import json
import os
import sys
import urllib.request

def load_env(env_path):
    config = {}
    if os.path.exists(env_path):
        with open(env_path) as f:
            for line in f:
                line = line.strip()
                if line and not line.startswith("#") and "=" in line:
                    k, v = line.split("=", 1)
                    config[k.strip()] = v.strip().strip("\"'")
    return config

def main():
    parser = argparse.ArgumentParser(description="Send safe test alert to a specific developer address.")
    parser.add_argument("--to", default="cam@camcamcam.cam", help="Recipient email address")
    parser.add_argument("--pub", default="The Grandview", help="Pub name for test alert")
    args = parser.parse_args()

    env_path = os.path.join(os.path.dirname(__file__), "..", ".env")
    cfg = load_env(env_path)
    api_key = cfg.get("RESEND_API_KEY")
    if not api_key:
        print("Error: RESEND_API_KEY not found in .env")
        sys.exit(1)

    print(f"★ Sending SAFE test alert to {args.to} (zero broadcast)...")

    subject = f"★ [TEST] Cam is at {args.pub}!"
    html_body = f"""<!DOCTYPE html>
<html>
<body style="font-family: sans-serif; background: #faf4eb; color: #231309; padding: 24px;">
  <div style="max-width: 480px; margin: 0 auto; border: 4px solid #231309; border-radius: 18px; padding: 24px; text-align: center;">
    <div style="font-size: 13px; font-weight: bold; letter-spacing: 0.15em; color: #543420;">★ whereis.cam (TEST ALERT) ★</div>
    <div style="display: inline-block; background: #1c532b; color: #faf4eb; font-weight: bold; padding: 6px 14px; border-radius: 20px; font-size: 12px; margin: 16px 0;">TEST ALERT &bull; CAM AT PUB</div>
    <h1 style="font-family: Georgia, serif; font-size: 32px; color: #ba3f0c; margin: 0 0 10px;">{args.pub}</h1>
    <div style="font-size: 14px; color: #543420; margin-bottom: 20px;">🕒 First pot poured at 6:42 PM AEDT</div>
    <p style="font-size: 13px; color: #754c30;">This was a safe developer test sent exclusively to {args.to}.</p>
  </div>
</body>
</html>"""

    payload = {
        "from": "Cam's Pub Radar <alerts@whereis.cam>",
        "to": [args.to],
        "subject": subject,
        "html": html_body
    }

    req = urllib.request.Request("https://api.resend.com/emails", headers={
        "Authorization": f"Bearer {api_key}",
        "Content-Type": "application/json",
        "User-Agent": "whereis.cam/1.0"
    }, data=json.dumps(payload).encode("utf-8"))

    try:
        with urllib.request.urlopen(req) as resp:
            data = json.loads(resp.read().decode())
            print(f"✓ Success! Test email accepted by Resend (ID: {data.get('id')})")
    except Exception as e:
        print(f"✗ Failed to send test alert: {e}")
        sys.exit(1)

if __name__ == "__main__":
    main()
