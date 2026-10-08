# 🌐 DNS & Domain Setup Guide for whereis.cam

This guide walks through configuring DNS records and SSL/TLS certificates for **`whereis.cam`** so your friends can access it securely at `https://whereis.cam`.

---

## 📋 Recommended DNS Records

Regardless of which DNS provider or registrar you use (e.g. Cloudflare, Namecheap, Porkbun, Google Domains), you will configure records for:
1. **Root Domain (`@` or `whereis.cam`)**
2. **Subdomain (`www.whereis.cam`)**

---

## 🚀 Deployment Options

Choose the deployment method that matches your infrastructure:

### Option 1: Cloudflare Tunnel (Zero-Port Homelab / Self-Hosted) ⭐ *Recommended if hosting alongside Home Assistant*

If you are running the `whereiscam` binary on the same machine or local network as your Home Assistant server, a **Cloudflare Tunnel** (`cloudflared`) is the fastest and most secure approach. It requires **no open ports** or port-forwarding on your router.

1. **Add `whereis.cam` to Cloudflare**:
   - Change your domain's nameservers at your registrar to the Cloudflare nameservers provided.
2. **Create a Cloudflare Tunnel**:
   - In the [Cloudflare Zero Trust Dashboard](https://one.dash.cloudflare.com/) > **Networks** > **Tunnels** > **Create a tunnel**.
   - Install `cloudflared` on your host machine following the on-screen command.
3. **Add Public Hostname**:
   - **Subdomain**: leave empty (or `@`)
   - **Domain**: `whereis.cam`
   - **Type**: `HTTP`
   - **URL**: `localhost:8080` (or the IP of the machine running `whereiscam`)
   - Add another hostname for `www.whereis.cam` if desired.
4. **Result**:
   - Cloudflare automatically routes DNS, manages edge SSL certificates, and proxies traffic to your local server without exposing your IP address.

---

### Option 2: VPS / Dedicated Server with Caddy Reverse Proxy ⭐ *Easiest for standard VPS (Hetzner, DigitalOcean, Linode)*

If you run the binary on a Linux VPS:

#### 1. Configure DNS Records in your DNS Management Panel:
| Type | Name / Host | Content / Value | TTL | Note |
|---|---|---|---|---|
| **A** | `@` | `<YOUR_SERVER_IPV4>` | Auto / 300 | Points `whereis.cam` to your server IPv4 |
| **AAAA** | `@` | `<YOUR_SERVER_IPV6>` | Auto / 300 | *(Optional)* Points to IPv6 address |
| **CNAME** | `www` | `whereis.cam` | Auto / 300 | Points `www.whereis.cam` to root domain |

#### 2. Reverse Proxy with Caddy:
[Caddy](https://caddyserver.com/) automatically manages Let's Encrypt SSL/TLS certificates and handles HTTP-to-HTTPS redirects out of the box with zero boilerplate.

Install Caddy, then add this to `/etc/caddy/Caddyfile`:

```caddy
whereis.cam, www.whereis.cam {
    reverse_proxy 127.0.0.1:8080
}
```

Reload Caddy:
```bash
sudo systemctl reload caddy
```
Caddy will automatically fetch TLS certificates for `whereis.cam` and serve your site securely over HTTPS.

---

### Option 3: VPS with Nginx + Certbot

If you prefer Nginx:

1. Point your **A** record for `@` and **CNAME** for `www` to your server's IP.
2. Add Nginx server block (`/etc/nginx/sites-available/whereis.cam`):
   ```nginx
   server {
       listen 80;
       listen [::]:80;
       server_name whereis.cam www.whereis.cam;

       location / {
           proxy_pass http://127.0.0.1:8080;
           proxy_set_header Host $host;
           proxy_set_header X-Real-IP $remote_addr;
           proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
           proxy_set_header X-Forwarded-Proto $scheme;
       }
   }
   ```
3. Enable the site and request SSL certificate:
   ```bash
   sudo ln -s /etc/nginx/sites-available/whereis.cam /etc/nginx/sites-enabled/
   sudo nginx -t && sudo systemctl reload nginx
   sudo certbot --nginx -d whereis.cam -d www.whereis.cam
   ```

---

### Option 4: PaaS (Fly.io)

If you deploy using [Fly.io](https://fly.io):

1. Allocate IPs:
   ```bash
   fly ips allocate-v4
   fly ips allocate-v6
   ```
2. Create certificate:
   ```bash
   fly certs create whereis.cam
   fly certs create www.whereis.cam
   ```
3. Add the DNS records Fly outputs to your DNS provider:
   - **A** record for `@` pointing to your allocated IPv4
   - **AAAA** record for `@` pointing to your allocated IPv6
   - **CNAME** for `www` pointing to `<your-app-name>.fly.dev`

---

## 🔍 How to Test DNS & Connectivity

### 1. Check DNS Propagation
Run from your terminal:
```bash
# Check A record resolution
dig whereis.cam +short

# Query authoritative nameservers directly
dig @1.1.1.1 whereis.cam +short

# Check www CNAME
dig www.whereis.cam +short
```

### 2. Test HTTP/HTTPS Handshake
```bash
curl -Iv https://whereis.cam
```
You should see HTTP `200 OK` with a valid TLS certificate issued.

### 3. Test API Response
```bash
curl -s https://whereis.cam/api/status
```
Should return:
```json
{"at_pub":false,"headline":"Cam's not at a pub. Weird.", ...}
```
