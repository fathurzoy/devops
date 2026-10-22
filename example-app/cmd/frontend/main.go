// frontend: menyajikan halaman belajar (FE1 / FE2 dari codebase yang sama,
// dibedakan env FE_ID). FE hanya bicara ke api-gateway, tidak pernah FE-ke-FE langsung.
package main

import (
	"net/http"
	"strings"

	"exampleapp/internal/httpx"
)

func main() {
	httpx.ServiceName = httpx.Env("SERVICE_NAME", "frontend")
	feID := httpx.Env("FE_ID", "fe1")
	title, peerID, peerLabel, color, desc := "FE1 — Aplikasi User/Pelanggan", "fe2", "FE2 — Dashboard Admin/Toko", "#2563eb", "Sisi pelanggan: buat user, buat order, kirim pesan ke admin, pantau status order."
	if feID == "fe2" {
		httpx.ServiceName = httpx.Env("SERVICE_NAME", "frontend")
		title, peerID, peerLabel, color, desc = "FE2 — Dashboard Admin/Toko", "fe1", "FE1 — Aplikasi User/Pelanggan", "#7c3aed", "Sisi admin/toko: pantau order masuk, setujui/tolak order, balas pesan ke pelanggan."
	}
	page := strings.NewReplacer(
		"__FE_ID__", feID,
		"__FE_TITLE__", title,
		"__PEER_ID__", peerID,
		"__PEER_LABEL__", peerLabel,
		"__FE_COLOR__", color,
		"__FE_DESC__", desc,
		"__GW_PORT__", httpx.Env("GATEWAY_PUBLIC_PORT", "18080"),
		"__MQ_PORT__", httpx.Env("RABBITMQ_PUBLIC_PORT", "15673"),
	).Replace(pageHTML)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", httpx.HealthHandler)
	mux.HandleFunc("GET /metrics", httpx.MetricsHandler)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	})
	port := httpx.Env("PORT", "8080")
	httpx.Log("info", "", "frontend "+feID+" listen :"+port)
	if err := http.ListenAndServe(":"+port, httpx.Middleware(mux)); err != nil {
		panic(err)
	}
}

const pageHTML = `<!DOCTYPE html>
<html lang="id"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>__FE_TITLE__ — Example App DevOps</title>
<style>
body{font-family:system-ui,sans-serif;margin:0;background:#0f172a;color:#e2e8f0}
header{background:__FE_COLOR__;padding:18px 22px}
header h1{margin:0;font-size:21px} header p{margin:5px 0 0;opacity:.92;font-size:13.5px}
main{max-width:1020px;margin:0 auto;padding:16px}
section{background:#1e293b;border:1px solid #334155;border-radius:10px;padding:14px 16px;margin:13px 0}
h2{margin:2px 0 8px;font-size:16.5px} .note{color:#94a3b8;font-size:13px;line-height:1.5}
button{background:__FE_COLOR__;color:#fff;border:0;border-radius:7px;padding:8px 13px;margin:3px 3px 3px 0;cursor:pointer;font-size:13.5px}
button:hover{opacity:.87} button.alt{background:#475569} button.danger{background:#dc2626} button.ok{background:#16a34a}
input{background:#0f172a;color:#e2e8f0;border:1px solid #475569;border-radius:6px;padding:7px 9px;margin:3px 3px 3px 0;font-size:13.5px}
label{font-size:12.5px;color:#94a3b8;display:block;margin-top:5px}
pre{background:#020617;border:1px solid #334155;border-radius:8px;padding:11px;overflow:auto;font-size:12px;max-height:330px;white-space:pre-wrap;word-break:break-word}
#meta{font-size:13px;margin:8px 0;color:#fbbf24;word-break:break-all}
table{border-collapse:collapse;width:100%;font-size:13px} td,th{border-bottom:1px solid #334155;padding:6px 7px;text-align:left}
.badge{padding:2px 8px;border-radius:20px;font-size:11.5px;background:#475569;color:#fff}
.CREATED{background:#64748b}.APPROVED{background:#16a34a}.REJECTED{background:#dc2626}.DONE{background:#2563eb}
ul.feed{list-style:none;padding:0;margin:6px 0} ul.feed li{border-bottom:1px solid #334155;padding:7px 2px;font-size:13px;line-height:1.5}
.small{font-size:11.5px;color:#94a3b8;word-break:break-all}
</style></head><body>
<header><h1>__FE_TITLE__</h1><p>__FE_DESC__<br>Frontend ini (<b>__FE_ID__</b>) hanya memanggil <b>api-gateway</b>. Ia <b>tidak pernah</b> bicara langsung ke frontend lain — semua interaksi antar-FE lewat backend + event RabbitMQ.<br>Environment: <b id="envInfo">memuat...</b></p></header>
<main>
<div id="meta">Belum ada request. Klik tombol mana saja, hasilnya muncul di panel output paling bawah tiap section (panel bersama).</div>
<button class="alt" onclick="copyLoki()">Salin Query Loki untuk trace_id terakhir</button>
<span class="small">Tempel query-nya di Grafana &rarr; Explore &rarr; datasource Loki untuk menelusuri 1 request di semua service.</span>

<section><h2>A. Health Semua Service</h2>
<p class="note">Gateway memanggil /health ke user-service, order-service, notification-service dengan satu trace_id yang sama.</p>
<button onclick="callAPI('Health Semua','GET','/api/health/all')">Cek Health Semua Service</button></section>

<section><h2>B. User &amp; Order — PostgreSQL + Antar-Service + Redis Cache</h2>
<label>Nama user</label><input id="uName" value="Bro Test">
<label>Email</label><input id="uEmail" value="bro@test.id">
<button onclick="createUser()">Buat User (gateway &rarr; user-service &rarr; PostgreSQL)</button>
<button onclick="callAPI('List Users','GET','/api/users',null,true)">Lihat Semua User (klik 2x: pertama MISS dari DB, kedua HIT dari Redis)</button>
<label>ID user untuk detail</label><input id="uId" value="1" size="5">
<button onclick="callAPI('Detail User','GET','/api/users/'+val('uId'))">Detail User per ID</button>
<hr style="border-color:#334155">
<label>ID user pembeli</label><input id="oUser" value="1" size="5">
<label>Item</label><input id="oItem" value="Kopi Susu">
<label>Qty</label><input id="oQty" value="2" size="4">
<button onclick="createOrder()">Buat Order (gateway &rarr; order &rarr; validasi user &rarr; PostgreSQL &rarr; RabbitMQ)</button>
<button onclick="refreshOrders(true)">Lihat Semua Order</button>
<div id="ordersTable" style="margin-top:8px"></div>
<p class="note">Order baru otomatis muncul di tabel ini &amp; di FE lain (polling tiap 5 detik). Di FE2, tiap order punya tombol Setujui/Tolak.</p></section>

<section><h2>C. Redis — Cache, SET/GET, Counter</h2>
<label>Key</label><input id="rKey" value="demo:kunci">
<label>Value</label><input id="rVal" value="halo-redis">
<button onclick="callAPI('Redis SET','POST','/api/cache',{key:val('rKey'),value:val('rVal'),ttl:60})">SET ke Redis (TTL 60 detik)</button>
<button onclick="callAPI('Redis GET','GET','/api/cache?key='+encodeURIComponent(val('rKey')))">GET dari Redis</button>
<button onclick="callAPI('Counter INCR','POST','/api/cache/counter',{})">Naikkan Counter (INCR)</button>
<button onclick="callAPI('Counter GET','GET','/api/cache/counter')">Lihat Counter</button></section>

<section><h2>D. RabbitMQ — Publish &amp; Consume Manual</h2>
<label>Teks pesan</label><input id="mqText" value="Halo RabbitMQ dari FE">
<button onclick="callAPI('Publish RabbitMQ','POST','/api/rabbitmq/publish',{text:val('mqText'),from:'__FE_ID__',target:'all'})">Kirim Pesan ke RabbitMQ (queue manual.events)</button>
<button onclick="callAPI('Pesan Terkirim','GET','/api/notifications')">Lihat Pesan yang Sudah Dikonsumsi Notification Service</button>
<p class="note">Management UI RabbitMQ environment ini: port __MQ_PORT__ di alamat server ini (login dari file secrets.env environment ini di server).</p></section>

<section><h2>E. Simulasi — Error, Lambat, Spam (bahan Grafana)</h2>
<button class="danger" onclick="callAPI('Simulasi Error','GET','/api/simulate/error')">Simulasi Error 500 (order-service)</button>
<button class="alt" onclick="callAPI('Simulasi Slow','GET','/api/simulate/slow?ms=2000')">Simulasi Slow 2 Detik</button>
<button class="alt" onclick="spam()">Spam 20x Request (lihat grafik traffic di Grafana)</button></section>

<section><h2>F. Interaksi __FE_ID__ &#8596; __PEER_LABEL__ (lewat Backend + RabbitMQ)</h2>
<p class="note">Alurnya: FE ini &rarr; gateway &rarr; notification-service publish event ke RabbitMQ (queue fe.events) &rarr; notification-service konsumsi &amp; simpan &rarr; FE tujuan membaca lewat endpoint /api/events (polling). Tidak ada koneksi FE-ke-FE langsung.</p>
<label>Pesan untuk __PEER_LABEL__</label><input id="peerText" value="Halo dari __FE_ID__!" style="width:60%">
<button onclick="sendPeer()">Kirim Pesan ke __PEER_ID__</button>
<button class="alt" onclick="sendBroadcast()">Broadcast ke Semua FE</button>
<h3 style="margin:13px 0 4px;font-size:14.5px">Pesan Masuk untuk __FE_ID__ (dari FE lain / broadcast / event order)</h3>
<button class="alt" onclick="refreshInbox(true)">Refresh Manual</button>
<ul class="feed" id="inbox"><li class="small">Memuat...</li></ul></section>

<section><h2>G. Scheduler Terpusat — 1 Penjadwal untuk Semua Service</h2>
<p class="note">Dulu tiap service memasang scheduler sendiri &rarr; saat replica banyak, job jalan dobel &amp; bentrok. Di sini hanya <b>scheduler-service</b> yang menjadwalkan: tiap interval ia <b>menembak API</b> order-service (<code>POST /jobs/cleanup-demo</code>, endpoint internal yang tidak dibuka lewat gateway). Sebelum jalan ia mengambil <b>lock di Redis</b>; kalau job sebelumnya masih jalan, eksekusi berikutnya <b>dilewati (SKIPPED_LOCKED)</b>.</p>
<div id="schedInfo" class="note">Memuat status scheduler...</div>
<button onclick="runSched(3000)">Jalankan Sekarang (normal 3 detik)</button>
<button class="alt" onclick="runSchedSlow()">Jalankan Job Lambat (15 detik) lalu Coba Lagi</button>
<button class="alt" onclick="refreshSched(true)">Refresh Status</button>
<div id="schedTable" style="margin-top:8px"></div></section>

<section><h2>H. HA — Replica &amp; Load Balancing Order-Service</h2>
<p class="note">Order-service bisa jalan lebih dari 1 replica di belakang gateway; gateway memilih upstream bergantian (round-robin) dan me-resolve DNS per request, jadi replica berganti-ganti yang menjawab. Tombol ini memukul <code>/whoami</code> 10x dan menampilkan replica (hostname) + versi yang menjawab.</p>
<button onclick="haCheck()">Hit Order-Service 10x — Replica Mana Saja yang Menjawab?</button>
<div id="haOut" style="margin-top:8px"></div></section>

<section><h2>Panel Output</h2><pre id="out">// Hasil request terakhir tampil di sini (JSON + status + durasi + trace_id)</pre></section>
</main>
<script>
var GW = 'http://' + location.hostname + ':__GW_PORT__';
var MY = '__FE_ID__';
var lastTrace = '';
function val(id){ return document.getElementById(id).value; }
function show(title, status, ms, trace, data){
  lastTrace = trace || lastTrace;
  document.getElementById('meta').textContent = title + ' | HTTP ' + status + ' | ' + ms + ' ms | trace_id: ' + (trace || '-');
  document.getElementById('out').textContent = (typeof data === 'string') ? data : JSON.stringify(data, null, 2);
}
function copyLoki(){
  if(!lastTrace){ alert('Belum ada trace_id. Klik tombol request dulu.'); return; }
  var q = '{container=~"example-.+"} |= "' + lastTrace + '"';
  navigator.clipboard.writeText(q).then(function(){ alert('Query Loki tersalin:\n' + q); });
}
async function callAPI(title, method, path, body, alsoOrders){
  var t0 = performance.now();
  try{
    var opt = {method: method, headers: {'Content-Type':'application/json'}};
    if(body) opt.body = JSON.stringify(body);
    var res = await fetch(GW + path, opt);
    var txt = await res.text();
    var data; try{ data = JSON.parse(txt); }catch(e){ data = txt; }
    var trace = (data && data.trace_id) || res.headers.get('X-Trace-Id') || '';
    show(title, res.status, Math.round(performance.now()-t0), trace, data);
    if(alsoOrders) refreshOrders(false);
    return {status: res.status, data: data, trace: trace};
  }catch(e){
    show(title, 'NETWORK ERROR', Math.round(performance.now()-t0), '', 'Gagal fetch ke gateway ' + GW + ' : ' + e);
    return null;
  }
}
async function createUser(){
  var r = await callAPI('Buat User','POST','/api/users',{name:val('uName'),email:val('uEmail')});
  if(r && r.data && r.data.user){ document.getElementById('uId').value = r.data.user.id; document.getElementById('oUser').value = r.data.user.id; }
}
async function createOrder(){
  var r = await callAPI('Buat Order','POST','/api/orders',{user_id:parseInt(val('oUser')),item:val('oItem'),qty:parseInt(val('oQty'))});
  if(r) refreshOrders(false);
}
async function setStatus(id, status){
  var r = await callAPI('Ubah Status Order #' + id,'POST','/api/orders/' + id + '/status',{status:status, actor:MY});
  if(r) refreshOrders(false);
}
async function refreshOrders(manual){
  try{
    var res = await fetch(GW + '/api/orders');
    var d = await res.json();
    var orders = d.orders || [];
    var html = '<table><tr><th>ID</th><th>User</th><th>Item</th><th>Qty</th><th>Status</th><th>trace_id</th>' + (MY==='fe2' ? '<th>Aksi (FE2)</th>' : '') + '</tr>';
    orders.slice(0,15).forEach(function(o){
      html += '<tr><td>' + o.id + '</td><td>' + o.user_id + '</td><td>' + o.item + '</td><td>' + o.qty + '</td><td><span class="badge ' + o.status + '">' + o.status + '</span></td><td class="small">' + (o.trace_id||'') + '</td>';
      if(MY==='fe2'){ html += '<td><button class="ok" onclick="setStatus(' + o.id + ',\'APPROVED\')">Setujui</button> <button class="danger" onclick="setStatus(' + o.id + ',\'REJECTED\')">Tolak</button></td>'; }
      html += '</tr>';
    });
    html += '</table>';
    document.getElementById('ordersTable').innerHTML = orders.length ? html : '<p class="note">Belum ada order.</p>';
    if(manual) show('Lihat Orders', res.status, 0, d.trace_id, d);
  }catch(e){ if(manual) show('Lihat Orders','ERROR',0,'',String(e)); }
}
async function sendPeer(){
  return callAPI('Kirim Pesan ke __PEER_ID__','POST','/api/events',{from:MY, target:'__PEER_ID__', kind:'message', text:val('peerText')});
}
async function sendBroadcast(){
  return callAPI('Broadcast','POST','/api/events',{from:MY, target:'all', kind:'broadcast', text:val('peerText')});
}
async function refreshInbox(manual){
  try{
    var res = await fetch(GW + '/api/events?target=' + MY);
    var d = await res.json();
    var evs = d.events || [];
    var html = '';
    evs.slice(0,15).forEach(function(e){
      html += '<li><b>' + (e.kind||'event') + '</b> dari <b>' + (e.from||'-') + '</b> &rarr; ' + (e.text||'') + '<br><span class="small">' + (e.received_at||'') + ' | trace_id: ' + (e.trace_id||'-') + '</span></li>';
    });
    document.getElementById('inbox').innerHTML = html || '<li class="small">Belum ada pesan masuk untuk __FE_ID__.</li>';
    if(manual) show('Inbox __FE_ID__', res.status, 0, d.trace_id, d);
  }catch(e){ if(manual) show('Inbox','ERROR',0,'',String(e)); }
}
async function spam(){
  var t0 = performance.now(); var ok = 0;
  var jobs = [];
  for(var i=0;i<20;i++){ jobs.push(fetch(GW + '/api/users').then(function(r){ if(r.ok) ok++; }).catch(function(){})); }
  await Promise.all(jobs);
  show('Spam 20x', 200, Math.round(performance.now()-t0), lastTrace, 'Selesai: ' + ok + '/20 request OK ke GET /api/users. Buka Grafana (dashboard Node Exporter / cAdvisor atau Explore Prometheus: rate(http_requests_total[1m])) untuk melihat lonjakan traffic.');
}
async function refreshSched(manual){
  try{
    var res = await fetch(GW + '/api/scheduler/status');
    var d = await res.json();
    document.getElementById('schedInfo').innerHTML =
      'env: <b>' + d.env + '</b> | interval: <b>' + d.interval_sec + ' detik</b> | job: <b>' + d.job + '</b> &rarr; ' + d.target +
      ' | lock Redis <code>' + d.lock_key + '</code>: <b>' + (d.lock_held ? 'SEDANG TERPEGANG (ada job jalan)' : 'bebas') + '</b>';
    var runs = d.runs || [];
    var html = '<table><tr><th>Waktu (UTC)</th><th>Sumber</th><th>Status</th><th>Durasi</th><th>Replica</th><th>trace_id</th><th></th></tr>';
    runs.slice(0,12).forEach(function(x){
      var badge = x.status==='SUCCESS' ? 'APPROVED' : (x.status==='SKIPPED_LOCKED' ? 'CREATED' : (x.status==='RUNNING' ? 'DONE' : 'REJECTED'));
      html += '<tr><td>' + (x.started_at||'') + '</td><td>' + x.source + '</td><td><span class="badge ' + badge + '">' + x.status + '</span></td><td>' + Math.round(x.duration_ms||0) + ' ms</td><td>' + (x.replica||'-') + '</td><td class="small">' + (x.trace_id||'') + '</td><td><button class="alt" onclick="copyLokiTrace(\'' + (x.trace_id||'') + '\')">Loki</button></td></tr>';
    });
    html += '</table>';
    document.getElementById('schedTable').innerHTML = runs.length ? html : '<p class="note">Belum ada run. Tunggu tick pertama atau klik Jalankan Sekarang.</p>';
    if(manual) show('Status Scheduler', res.status, 0, d.trace_id, d);
  }catch(e){ if(manual) show('Status Scheduler','ERROR',0,'',String(e)); }
}
function copyLokiTrace(t){
  if(!t){ alert('trace_id kosong'); return; }
  var q = '{container=~"example-.+"} |= "' + t + '"';
  navigator.clipboard.writeText(q).then(function(){ alert('Query Loki tersalin. Trace ini muncul di log scheduler-service DAN order-service:\n' + q); });
}
async function runSched(ms){
  var r = await callAPI('Scheduler Run-Now ' + ms + 'ms','POST','/api/scheduler/run-now',{duration_ms:ms});
  refreshSched(false);
  return r;
}
async function runSchedSlow(){
  show('Job Lambat','...',0,'','Menjalankan job 15 detik (tidak ditunggu selesai di panel ini)...');
  fetch(GW + '/api/scheduler/run-now', {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({duration_ms:15000})}).catch(function(){});
  await new Promise(function(z){ setTimeout(z, 1200); });
  var r2 = await callAPI('Run Kedua (harus SKIPPED_LOCKED)','POST','/api/scheduler/run-now',{duration_ms:3000});
  refreshSched(false);
  if(r2 && r2.data && r2.data.run){ alert('Status run kedua: ' + r2.data.run.status + ' — kalau SKIPPED_LOCKED, berarti lock bekerja: job lambat yang masih jalan menahan eksekusi berikutnya.'); }
  setTimeout(refreshSched, 16000);
}
async function haCheck(){
  var t0 = performance.now();
  try{
    var res = await fetch(GW + '/api/ha/check?n=10');
    var d = await res.json();
    var html = '<p class="note">Upstream aktif: <b>' + (d.upstreams_aktif||[]).join(', ') + '</b> | total ' + d.total + ' hit, gagal ' + d.gagal + '</p>';
    html += '<table><tr><th>Replica (hostname) | Version</th><th>Jumlah Jawaban</th></tr>';
    Object.keys(d.ringkasan||{}).forEach(function(k){ html += '<tr><td>' + k + '</td><td>' + d.ringkasan[k] + '</td></tr>'; });
    html += '</table>';
    document.getElementById('haOut').innerHTML = html;
    show('HA Check 10x', res.status, Math.round(performance.now()-t0), d.trace_id, d);
  }catch(e){ show('HA Check','ERROR',0,'',String(e)); }
}
async function loadEnvInfo(){
  try{
    var res = await fetch(GW + '/api/health/all');
    var d = await res.json();
    var gw = d.services && d.services['api-gateway'];
    if(gw) document.getElementById('envInfo').textContent = gw.env + ' | versi gateway ' + gw.version;
  }catch(e){}
}
refreshOrders(false); refreshInbox(false); refreshSched(false); loadEnvInfo();
setInterval(refreshOrders, 5000); setInterval(refreshInbox, 4000); setInterval(refreshSched, 5000);
</script></body></html>`
