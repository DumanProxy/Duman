# Duman Threat Model

## Assets

| Asset | Description | Sensitivity |
|-------|-------------|-------------|
| User traffic | Actual internet traffic being tunneled | High |
| Shared secret | Pre-shared key for tunnel encryption | Critical |
| Session keys | Derived per-session encryption keys | Critical |
| Relay addresses | IP/domain of relay servers | Medium |
| Client identity | User's IP and connection metadata | High |
| Tunnel metadata | Stream IDs, sequence numbers, timing | Medium |

## Threat Actors

### T1: Passive ISP Observer
**Capability**: Full visibility of all traffic crossing the network. Can log, store, and analyze packet captures. Statistical analysis capability.

**Goals**: Identify users bypassing censorship. Detect non-standard protocol usage.

**Mitigations**:
- Perfect wire protocol implementation (psql/DBeaver/mysql CLI compatible)
- Cover queries interleaved with tunnel queries at natural timing
- EWMA-controlled cover:tunnel ratio matches real application patterns
- Kolmogorov-Smirnov tested traffic indistinguishability (p > 0.05)
- Multiple noise layers (phantom browser, P2P smokescreen, decoy HTTPS)
- Dynamic schema mutations ensure no two sessions have identical fingerprints

**Residual Risk**: Long-term traffic volume analysis may reveal patterns over months. Persistent surveillance of the specific relay IP may flag frequent connections.

### T2: Active ISP with DPI
**Capability**: Deep packet inspection, active probing, TCP/TLS injection, protocol-aware firewalls. Can send probe queries to suspected servers.

**Goals**: Confirm server is a tunnel relay, not a real database.

**Mitigations**:
- Relay responds to ALL queries as a real database (unauthenticated users get realistic fake data)
- SCRAM-SHA-256 authentication (industry standard)
- Full pg_catalog/information_schema metadata support
- EXPLAIN, SET, BEGIN/COMMIT/ROLLBACK all handled
- Server version string matches real PostgreSQL 16.1
- TLS 1.2+ with standard cipher suites
- No protocol deviations detectable by active probing
- Certificate pinning prevents MITM

**Residual Risk**: Extremely sophisticated DPI that compares response patterns against a known real PostgreSQL instance may find subtle timing differences. Active prober could attempt application-level consistency checks (e.g., INSERT then verify data persists across connections).

### T3: Active Prober / Censor
**Capability**: Can connect to relay, send arbitrary queries, analyze responses for inconsistencies.

**Goals**: Determine if server is a real database or a tunnel.

**Mitigations**:
- Dynamic schema with realistic fake data (hundreds of rows per table)
- INSERT/UPDATE/DELETE properly acknowledged
- Data appears consistent within a session
- Foreign key relationships maintained in fake data
- Aggregate queries (COUNT, SUM, AVG) return consistent results
- Canary token detection alerts on tracking patterns

**Residual Risk**: Cross-session consistency not guaranteed (schema mutations change between sessions). A prober connecting multiple times may notice different table/column names. Mitigation: use fixed schema mode for targeted probing environments.

### T4: Relay Compromise
**Capability**: Full access to relay server, can read memory, modify code, log all traffic.

**Goals**: Identify users, decrypt traffic, inject content.

**Mitigations**:
- Relay processes encrypted chunks but cannot read plaintext (encryption is end-to-end from client to destination via the relay's exit engine — but relay IS the exit point)
- **Important**: In current architecture, relay IS the exit node and CAN see plaintext traffic after decryption. This is a fundamental architectural property, not a vulnerability.
- PFS (X25519 ephemeral keys) ensures past sessions cannot be decrypted even with compromised long-term key
- Chain forwarding mode: relay only sees encrypted chunks, forwards to exit relay
- Rate limiting prevents abuse of compromised relay

**Residual Risk**: A compromised exit relay can see all plaintext traffic. Mitigation: use chain mode with trusted exit relays. Users should use HTTPS for sensitive destinations regardless.

### T5: Endpoint Compromise
**Capability**: Full access to client machine.

**Goals**: Extract shared secret, identify relay connections, log plaintext.

**Mitigations**:
- Config file permissions warning at startup
- Keys never logged (enforced by security tests)
- Memory zeroing for sensitive key material
- Kill switch prevents traffic leak if client crashes

**Residual Risk**: Full endpoint compromise defeats all tunnel protections. This is true for any privacy tool. OS-level security is a prerequisite.

### T6: Timing Correlation Attack
**Capability**: Can observe both client's ISP and relay's ISP simultaneously.

**Goals**: Correlate client traffic with relay's outbound connections.

**Mitigations**:
- Timing jitter injection (configurable 0-50ms random delay)
- Traffic padding to fixed sizes (16KB chunks)
- Cover queries add noise to timing patterns
- Bandwidth governor smooths traffic bursts
- Multiple relay rotation makes correlation harder

**Residual Risk**: Sophisticated global adversary with access to both ends can still perform correlation over extended periods. This is a known limitation of all low-latency proxy systems.

### T7: Volume Analysis
**Capability**: Can measure total traffic volume to/from relay over time.

**Goals**: Correlate download/upload volumes with known user activity.

**Mitigations**:
- Noise layers (phantom, P2P, decoy) add significant background traffic
- Bandwidth governor maintains consistent total volume
- Cover queries consume bandwidth even when tunnel is idle
- Multiple relays distribute traffic volume

**Residual Risk**: If tunnel traffic significantly exceeds noise traffic, volume correlation is possible. Paranoid mode trades bandwidth for maximum noise.

## STRIDE Analysis

| Threat | Category | Component | Mitigation | Status |
|--------|----------|-----------|------------|--------|
| Impersonation of relay | Spoofing | Provider | TLS certificate pinning | Implemented |
| Modified tunnel chunks | Tampering | Crypto | AEAD (ChaCha20-Poly1305/AES-GCM) with AAD | Implemented |
| User claims no tunnel use | Repudiation | Protocol | No logging of user activity on relay (feature) | By design |
| Traffic content exposure | Info Disclosure | Crypto | Per-session HKDF keys + optional PFS | Implemented |
| Relay overload | DoS | Relay | Per-IP rate limiting + circuit breaker | Implemented |
| Bypass kill switch | Elevation | Proxy | Fail-closed design, routing-level enforcement | Implemented |
| Replay attack | Tampering | Crypto | Sequence numbers in AAD, HMAC auth tokens with timestamps | Implemented |
| DNS leak | Info Disclosure | Proxy | DNS cache + tunnel DNS for routed domains | Implemented |
| Key extraction from memory | Info Disclosure | Crypto | Memory zeroing, no key logging | Implemented |
| Protocol fingerprinting | Info Disclosure | Wire Protocol | Perfect protocol compliance + dynamic schemas | Implemented |

## Security Configuration Profiles

### Speed (Lowest Security)
```yaml
tunnel:
  padding: false
  jitter_ms: 0
noise:
  phantom_browser: false
  smoke_screen: false
crypto:
  pfs: false
  cipher: auto
```
Best throughput, minimal overhead. Suitable for low-risk environments.

### Balanced (Default)
```yaml
tunnel:
  padding: false
  jitter_ms: 10
noise:
  phantom_browser: true
  smoke_screen: false
crypto:
  pfs: true
  cipher: auto
```
Good balance of performance and security. PFS enabled.

### Stealth
```yaml
tunnel:
  padding: true
  jitter_ms: 25
noise:
  phantom_browser: true
  smoke_screen: true
  decoy: true
crypto:
  pfs: true
  cipher: chacha20
interleave:
  profile: casual_browser
  base_ratio: 5
```
Maximum stealth with all noise layers. Higher latency and bandwidth usage.

### Paranoid
```yaml
tunnel:
  padding: true
  jitter_ms: 50
noise:
  phantom_browser: true
  smoke_screen: true
  decoy: true
crypto:
  pfs: true
  cipher: chacha20
interleave:
  profile: casual_browser
  base_ratio: 8
pool:
  max_active: 5
  rotation_interval: 30s
```
Maximum security. Chain mode recommended. Significant performance cost.

## Known Limitations

1. **Low-latency system**: Like Tor, Duman is a low-latency system vulnerable to global traffic analysis. It is NOT designed to resist a global passive adversary with access to all network links.

2. **Exit relay visibility**: The exit relay can see plaintext traffic. Chain mode reduces this to a single trusted exit point.

3. **Database plausibility**: While fake data is realistic, extended interaction by a skilled DBA may reveal inconsistencies (e.g., business logic violations in data relationships).

4. **Cross-session fingerprinting**: Schema mutations change between sessions. An adversary monitoring relay connections over time may notice schema changes inconsistent with a real database.

5. **Binary size**: The Go binary contains all protocol implementations, which is larger than typical database clients. This is mitigable by stripping debug symbols.

6. **Bandwidth overhead**: Cover queries, noise layers, and padding consume significant bandwidth. In paranoid mode, useful throughput may be 10-20% of total bandwidth.
