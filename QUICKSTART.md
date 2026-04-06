# Duman Quick Start — Demo Setup

**Linux sunucuda relay, Windows'ta client. 5 dakikada çalışır.**

---

## 1. Shared Secret Oluştur (herhangi bir makinede)

```bash
# Go kuruluysa:
go run ./cmd/duman-client keygen

# Veya OpenSSL ile:
openssl rand -base64 32
```

Çıkan değeri kaydet — hem server hem client'a lazım.

---

## 2. Linux Sunucu — Relay Kurulumu

### Otomatik (önerilen)

```bash
# Binary'yi sunucuya kopyala
scp dist/duman-relay-linux-amd64 user@sunucu:/tmp/

# SSH ile bağlan ve kur
ssh user@sunucu
sudo bash -c '
  cp /tmp/duman-relay-linux-amd64 /usr/local/bin/duman-relay
  chmod +x /usr/local/bin/duman-relay
  duman-relay --version
'
```

### Ya da install script ile:

```bash
# Repo'yu sunucuya klonla veya script'i kopyala
sudo bash scripts/install-relay.sh --from-local /tmp/duman-relay-linux-amd64
```

### Manuel Config

`/etc/duman/relay.yaml` (veya çalışma dizininde `duman-relay.yaml`):

```yaml
listen:
  postgresql: ":5432"

auth:
  method: "md5"
  users:
    sensor_writer: "BURAYA_GUCLU_SIFRE"

tunnel:
  shared_secret: "BURAYA_SHARED_SECRET"
  role: "exit"

fake_data:
  scenario: "ecommerce"
  seed: 12345
  mode: "template"
  mutate: true

exit:
  max_idle_secs: 300

log:
  level: "info"
  format: "text"
  output: "stderr"
```

### Başlat

```bash
# Foreground (test için):
duman-relay -c /etc/duman/relay.yaml -v

# Systemd ile (install script kullandıysan):
sudo systemctl start duman-relay
sudo systemctl status duman-relay
sudo journalctl -u duman-relay -f
```

### Firewall

```bash
sudo ufw allow 5432/tcp   # UFW
# veya
sudo iptables -A INPUT -p tcp --dport 5432 -j ACCEPT
```

---

## 3. Windows Client — Kurulum

### Binary'yi indir/kopyala

`dist/duman-client-windows-amd64.exe` dosyasını istediğin yere koy.

### PowerShell ile kurulum:

```powershell
# Admin PowerShell aç, repo dizinine git
.\scripts\install-client.ps1 `
  -RelayAddress "SUNUCU_IP:5432" `
  -SharedSecret "BURAYA_SHARED_SECRET" `
  -Password "BURAYA_GUCLU_SIFRE"
```

### Manuel Config

`%APPDATA%\Duman\client.yaml` oluştur:

```yaml
proxy:
  listen: "127.0.0.1:1080"
  mode: "socks5"

tunnel:
  shared_secret: "BURAYA_SHARED_SECRET"
  chunk_size: 16384
  response_mode: "poll"
  cipher: "auto"

relays:
  - address: "SUNUCU_IP:5432"
    protocol: "postgresql"
    weight: 10
    database: "analytics"
    username: "sensor_writer"
    password: "BURAYA_GUCLU_SIFRE"

scenario: "ecommerce"

schema:
  mode: "template"
  mutate: true
  seed: 12345

log:
  level: "info"
  format: "text"
  output: "stderr"
```

### Başlat

```cmd
duman-client.exe -c %APPDATA%\Duman\client.yaml -v
```

Çıktıda şunu görmen lazım:
```
INFO  client ready  socks5=127.0.0.1:1080
INFO  SOCKS5 proxy listening  addr=127.0.0.1:1080
```

---

## 4. Test Et

### curl ile (SOCKS5 proxy üzerinden):

```bash
# Linux/Mac:
curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me

# Windows (Git Bash veya WSL):
curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me

# PowerShell:
# SOCKS5 doğrudan desteklenmiyor, browser kullan
```

### Browser ile:

**Firefox:**
1. Settings → Network Settings → Manual proxy
2. SOCKS Host: `127.0.0.1`, Port: `1080`
3. SOCKS v5 seç
4. "Proxy DNS when using SOCKS v5" işaretle
5. https://ifconfig.me → sunucunun IP'si gelmeli

**Chrome (komut satırı):**
```
chrome.exe --proxy-server="socks5://127.0.0.1:1080"
```

---

## 5. Sorun Giderme

| Sorun | Çözüm |
|-------|-------|
| `config error: proxy.listen is required` | Config dosyası bulunamıyor. `-c` ile yol ver |
| `connect relays: dial tcp: connection refused` | Sunucuda relay çalışıyor mu? Port açık mı? |
| `invalid tunnel auth token` | shared_secret eşleşmiyor — iki tarafta aynı olmalı |
| `auth failed` | username/password eşleşmiyor |
| SOCKS5'e bağlanılamıyor | Client çalışıyor mu? `127.0.0.1:1080` dinliyor mu? |
| Yanıt gelmiyor | Relay'de `exit.max_idle_secs` kontrol et |

### Debug modu:

```bash
# Relay:
duman-relay -c relay.yaml -v

# Client:
duman-client -c client.yaml -v
```

---

## Mimari

```
[Windows]                           [Linux Sunucu]                    [Internet]
                                                    
  Browser ──→ SOCKS5 ──→ duman-client ──→ PostgreSQL wire ──→ duman-relay ──→ Hedef site
  (1080)     Tunnel chunks hidden          (5432)            Exit engine
             inside INSERT/SELECT                            dials target
             statements                                      
```

Trafik, gerçek bir PostgreSQL analytics veritabanı gibi görünür.
DPI sistemleri INSERT INTO analytics_events ... görür — şifreli tunnel verisini değil.
