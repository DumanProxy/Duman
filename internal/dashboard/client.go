package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/pprof"
	"sync"
	"time"
)

// RelayStatus describes the current state of a relay connection.
type RelayStatus struct {
	Address  string        `json:"address"`
	Protocol string        `json:"protocol"`
	Status   string        `json:"status"` // healthy, failed, blocked
	Latency  time.Duration `json:"latency_ns"`
}

// ClientStats holds aggregated client-side metrics.
type ClientStats struct {
	Relays         []RelayStatus        `json:"relays"`
	TunnelStreams  int                  `json:"tunnel_streams"`
	Throughput     float64              `json:"throughput_bps"`
	CoverRate      float64              `json:"cover_rate_qps"`
	BandwidthAlloc map[string]float64   `json:"bandwidth_alloc"`
	NoiseStatus    map[string]bool      `json:"noise_status"`
	Uptime         time.Duration        `json:"uptime_ns"`
	StartTime      time.Time            `json:"start_time"`
}

// ClientDashboard serves a web dashboard for the Duman client.
type ClientDashboard struct {
	addr   string
	mux    *http.ServeMux
	server *http.Server
	stats  *ClientStats
	mu     sync.RWMutex

	// SSE subscribers
	sseClients map[chan []byte]struct{}
	sseMu      sync.Mutex
}

// NewClientDashboard creates a dashboard bound to the given address.
// Default address is 127.0.0.1:9090.
func NewClientDashboard(addr string) *ClientDashboard {
	if addr == "" {
		addr = "127.0.0.1:9090"
	}
	d := &ClientDashboard{
		addr: addr,
		mux:  http.NewServeMux(),
		stats: &ClientStats{
			BandwidthAlloc: make(map[string]float64),
			NoiseStatus:    make(map[string]bool),
			StartTime:      time.Now(),
		},
		sseClients: make(map[chan []byte]struct{}),
	}
	d.mux.HandleFunc("/", d.handleIndex)
	d.mux.HandleFunc("/api/stats", d.handleStats)
	d.mux.HandleFunc("/events", d.handleSSE)
	// pprof endpoints for production profiling
	d.mux.HandleFunc("/debug/pprof/", pprof.Index)
	d.mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	d.mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	d.mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	d.mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	d.server = &http.Server{
		Addr:    addr,
		Handler: d.mux,
	}
	return d
}

// Start begins serving the dashboard. It blocks until the context is cancelled
// or an error occurs.
func (d *ClientDashboard) Start(ctx context.Context) error {
	// Push SSE updates every 2 seconds
	go d.sseBroadcastLoop(ctx)

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		d.server.Shutdown(shutCtx)
	}()

	if err := d.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// UpdateStats replaces the current stats snapshot.
func (d *ClientDashboard) UpdateStats(stats ClientStats) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stats = &stats
}

// Addr returns the configured listen address.
func (d *ClientDashboard) Addr() string {
	return d.addr
}

func (d *ClientDashboard) currentStatsJSON() ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	s := *d.stats
	s.Uptime = time.Since(s.StartTime)
	return json.Marshal(s)
}

func (d *ClientDashboard) handleStats(w http.ResponseWriter, r *http.Request) {
	data, err := d.currentStatsJSON()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func (d *ClientDashboard) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan []byte, 4)
	d.sseMu.Lock()
	d.sseClients[ch] = struct{}{}
	d.sseMu.Unlock()

	defer func() {
		d.sseMu.Lock()
		delete(d.sseClients, ch)
		d.sseMu.Unlock()
	}()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case data := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (d *ClientDashboard) sseBroadcastLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			data, err := d.currentStatsJSON()
			if err != nil {
				continue
			}
			d.sseMu.Lock()
			for ch := range d.sseClients {
				select {
				case ch <- data:
				default:
					// Drop if subscriber is slow
				}
			}
			d.sseMu.Unlock()
		}
	}
}

func (d *ClientDashboard) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(clientHTML))
}

const clientHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Duman Client Dashboard</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:system-ui,-apple-system,sans-serif;background:#0f1117;color:#e0e0e0;padding:20px}
h1{color:#7eb8ff;margin-bottom:16px;font-size:1.5rem}
h2{color:#5ea4ff;margin-bottom:8px;font-size:1.1rem;border-bottom:1px solid #2a2e3a;padding-bottom:4px}
.grid{display:grid;grid-template-columns:1fr 1fr;gap:16px;margin-bottom:16px}
.card{background:#1a1d27;border:1px solid #2a2e3a;border-radius:8px;padding:16px}
.metric{font-size:1.8rem;font-weight:700;color:#7eb8ff}
.label{font-size:0.85rem;color:#888;margin-top:2px}
table{width:100%;border-collapse:collapse;font-size:0.9rem}
th,td{text-align:left;padding:6px 8px;border-bottom:1px solid #2a2e3a}
th{color:#888;font-weight:600}
.status-healthy{color:#4caf50}
.status-failed{color:#f44336}
.status-blocked{color:#ff9800}
.tag{display:inline-block;padding:2px 8px;border-radius:4px;font-size:0.8rem}
.tag-on{background:#1b3a1b;color:#4caf50}
.tag-off{background:#3a1b1b;color:#f44336}
#uptime{color:#aaa;font-size:0.85rem;margin-bottom:16px}
</style>
</head>
<body>
<h1>Duman Client</h1>
<div id="uptime"></div>
<div class="grid">
 <div class="card"><div class="metric" id="streams">0</div><div class="label">Tunnel Streams</div></div>
 <div class="card"><div class="metric" id="throughput">0 B/s</div><div class="label">Throughput</div></div>
 <div class="card"><div class="metric" id="coverRate">0 q/s</div><div class="label">Cover Query Rate</div></div>
 <div class="card"><div class="metric" id="relayCount">0</div><div class="label">Connected Relays</div></div>
</div>
<div class="card" style="margin-bottom:16px">
 <h2>Relays</h2>
 <table><thead><tr><th>Address</th><th>Protocol</th><th>Status</th><th>Latency</th></tr></thead><tbody id="relays"></tbody></table>
</div>
<div class="grid">
 <div class="card"><h2>Bandwidth Allocation</h2><div id="bw"></div></div>
 <div class="card"><h2>Noise Layers</h2><div id="noise"></div></div>
</div>
<script>
function fmtBytes(b){
 if(b>=1e9)return (b/1e9).toFixed(1)+' GB/s';
 if(b>=1e6)return (b/1e6).toFixed(1)+' MB/s';
 if(b>=1e3)return (b/1e3).toFixed(1)+' KB/s';
 return b.toFixed(0)+' B/s';
}
function fmtDur(ns){
 var s=ns/1e9;
 if(s<60)return s.toFixed(0)+'s';
 if(s<3600)return Math.floor(s/60)+'m '+Math.floor(s%60)+'s';
 var h=Math.floor(s/3600);s-=h*3600;
 return h+'h '+Math.floor(s/60)+'m';
}
function fmtLatency(ns){
 if(ns>=1e6)return (ns/1e6).toFixed(1)+'ms';
 return (ns/1e3).toFixed(0)+'us';
}
function update(d){
 document.getElementById('streams').textContent=d.tunnel_streams;
 document.getElementById('throughput').textContent=fmtBytes(d.throughput_bps);
 document.getElementById('coverRate').textContent=d.cover_rate_qps.toFixed(1)+' q/s';
 document.getElementById('uptime').textContent='Uptime: '+fmtDur(d.uptime_ns);
 var relays=d.relays||[];
 document.getElementById('relayCount').textContent=relays.length;
 var rb='';
 relays.forEach(function(r){
  rb+='<tr><td>'+r.address+'</td><td>'+r.protocol+'</td><td class="status-'+r.status+'">'+r.status+'</td><td>'+fmtLatency(r.latency_ns)+'</td></tr>';
 });
 document.getElementById('relays').innerHTML=rb;
 var bw=d.bandwidth_alloc||{};
 var bwh='';for(var k in bw){bwh+='<div>'+k+': '+(bw[k]*100).toFixed(1)+'%</div>';}
 document.getElementById('bw').innerHTML=bwh||'<div style="color:#888">No data</div>';
 var ns=d.noise_status||{};
 var nh='';for(var k in ns){nh+='<span class="tag '+(ns[k]?'tag-on':'tag-off')+'">'+k+': '+(ns[k]?'ON':'OFF')+'</span> ';}
 document.getElementById('noise').innerHTML=nh||'<div style="color:#888">No data</div>';
}
var es=new EventSource('/events');
es.onmessage=function(e){try{update(JSON.parse(e.data));}catch(ex){}};
fetch('/api/stats').then(function(r){return r.json();}).then(update).catch(function(){});
</script>
</body>
</html>`
