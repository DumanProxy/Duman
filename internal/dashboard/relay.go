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

// RelayStats holds aggregated relay-side metrics.
type RelayStats struct {
	ClientCount      int              `json:"client_count"`
	SessionDurations []time.Duration  `json:"session_durations_ns"`
	TunnelThroughput float64          `json:"tunnel_throughput_bps"`
	CoverQueryRate   float64          `json:"cover_query_rate_qps"`
	ExitPoolSize     int              `json:"exit_pool_size"`
	FakeEngineStats  map[string]int   `json:"fake_engine_stats"`
	Uptime           time.Duration    `json:"uptime_ns"`
	StartTime        time.Time        `json:"start_time"`
}

// RelayDashboard serves a web dashboard for the Duman relay.
type RelayDashboard struct {
	addr   string
	mux    *http.ServeMux
	server *http.Server
	stats  *RelayStats
	mu     sync.RWMutex

	sseClients map[chan []byte]struct{}
	sseMu      sync.Mutex
}

// NewRelayDashboard creates a relay dashboard bound to the given address.
// Default address is 127.0.0.1:9091.
func NewRelayDashboard(addr string) *RelayDashboard {
	if addr == "" {
		addr = "127.0.0.1:9091"
	}
	d := &RelayDashboard{
		addr: addr,
		mux:  http.NewServeMux(),
		stats: &RelayStats{
			FakeEngineStats: make(map[string]int),
			StartTime:       time.Now(),
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

// Start begins serving the relay dashboard.
func (d *RelayDashboard) Start(ctx context.Context) error {
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

// UpdateStats replaces the current relay stats snapshot.
func (d *RelayDashboard) UpdateStats(stats RelayStats) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stats = &stats
}

// Addr returns the configured listen address.
func (d *RelayDashboard) Addr() string {
	return d.addr
}

func (d *RelayDashboard) currentStatsJSON() ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	s := *d.stats
	s.Uptime = time.Since(s.StartTime)
	return json.Marshal(s)
}

func (d *RelayDashboard) handleStats(w http.ResponseWriter, r *http.Request) {
	data, err := d.currentStatsJSON()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func (d *RelayDashboard) handleSSE(w http.ResponseWriter, r *http.Request) {
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

func (d *RelayDashboard) sseBroadcastLoop(ctx context.Context) {
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
				}
			}
			d.sseMu.Unlock()
		}
	}
}

func (d *RelayDashboard) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(relayHTML))
}

const relayHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Duman Relay Dashboard</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:system-ui,-apple-system,sans-serif;background:#0f1117;color:#e0e0e0;padding:20px}
h1{color:#ffb87e;margin-bottom:16px;font-size:1.5rem}
h2{color:#ffa45e;margin-bottom:8px;font-size:1.1rem;border-bottom:1px solid #2a2e3a;padding-bottom:4px}
.grid{display:grid;grid-template-columns:1fr 1fr;gap:16px;margin-bottom:16px}
.card{background:#1a1d27;border:1px solid #2a2e3a;border-radius:8px;padding:16px}
.metric{font-size:1.8rem;font-weight:700;color:#ffb87e}
.label{font-size:0.85rem;color:#888;margin-top:2px}
table{width:100%;border-collapse:collapse;font-size:0.9rem}
th,td{text-align:left;padding:6px 8px;border-bottom:1px solid #2a2e3a}
th{color:#888;font-weight:600}
#uptime{color:#aaa;font-size:0.85rem;margin-bottom:16px}
</style>
</head>
<body>
<h1>Duman Relay</h1>
<div id="uptime"></div>
<div class="grid">
 <div class="card"><div class="metric" id="clients">0</div><div class="label">Connected Clients</div></div>
 <div class="card"><div class="metric" id="throughput">0 B/s</div><div class="label">Tunnel Throughput</div></div>
 <div class="card"><div class="metric" id="coverRate">0 q/s</div><div class="label">Cover Query Rate</div></div>
 <div class="card"><div class="metric" id="exitPool">0</div><div class="label">Exit Pool Size</div></div>
</div>
<div class="grid">
 <div class="card">
  <h2>Session Durations</h2>
  <table><thead><tr><th>#</th><th>Duration</th></tr></thead><tbody id="sessions"></tbody></table>
 </div>
 <div class="card">
  <h2>Fake Engine Stats</h2>
  <table><thead><tr><th>Table</th><th>Row Count</th></tr></thead><tbody id="fakeEngine"></tbody></table>
 </div>
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
function update(d){
 document.getElementById('clients').textContent=d.client_count;
 document.getElementById('throughput').textContent=fmtBytes(d.tunnel_throughput_bps);
 document.getElementById('coverRate').textContent=d.cover_query_rate_qps.toFixed(1)+' q/s';
 document.getElementById('exitPool').textContent=d.exit_pool_size;
 document.getElementById('uptime').textContent='Uptime: '+fmtDur(d.uptime_ns);
 var sessions=d.session_durations_ns||[];
 var sh='';
 sessions.forEach(function(ns,i){sh+='<tr><td>'+(i+1)+'</td><td>'+fmtDur(ns)+'</td></tr>';});
 document.getElementById('sessions').innerHTML=sh||'<tr><td colspan="2" style="color:#888">No sessions</td></tr>';
 var fe=d.fake_engine_stats||{};
 var fh='';for(var k in fe){fh+='<tr><td>'+k+'</td><td>'+fe[k]+'</td></tr>';}
 document.getElementById('fakeEngine').innerHTML=fh||'<tr><td colspan="2" style="color:#888">No data</td></tr>';
}
var es=new EventSource('/events');
es.onmessage=function(e){try{update(JSON.parse(e.data));}catch(ex){}};
fetch('/api/stats').then(function(r){return r.json();}).then(update).catch(function(){});
</script>
</body>
</html>`
