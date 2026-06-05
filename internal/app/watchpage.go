package app

import "fmt"

// watchPage renders the low-latency viewer page. It is state-aware: it subscribes to the SSE
// status feed (/api/events) and only shows the video player while the camera for this path is
// actually live. When the camera is not streaming it shows an "offline" panel with a link back
// to the SportsHub dashboard instead of trying to play a dead stream. When live it tries WebRTC
// (WHEP) for ~1s latency and falls back to (LL-)HLS.
func watchPage(streamPath, host string, webrtcPort int, hlsURL, rtmpURL string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Sportshub • %s</title>
  <style>
    body { font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background:#0a0a0a; color:#eee; margin:0; padding:0; }
    .topbar { display:flex; align-items:center; gap:14px; padding:12px 16px; border-bottom:1px solid #1c1c1c; position:sticky; top:0; background:#0a0a0a; }
    .back { color:#34d399; text-decoration:none; font-weight:600; font-size:0.9rem; white-space:nowrap; }
    .back:hover { text-decoration:underline; }
    .title { font-size:0.95rem; color:#ddd; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
    .wrap { max-width:980px; margin:0 auto; padding:16px; display:flex; flex-direction:column; align-items:center; }
    video { width:100%%; max-width:960px; background:#000; border-radius:12px; box-shadow:0 10px 30px rgba(0,0,0,0.6); }
    .meta { margin-top:12px; font-size:0.75rem; color:#666; word-break:break-all; text-align:center; }
    .btn { margin-top:12px; padding:10px 18px; background:#222; color:#ddd; border:1px solid #333; border-radius:8px; cursor:pointer; text-decoration:none; display:inline-block; }
    .btn:hover { background:#333; }
    .btn.primary { background:#059669; border-color:#059669; color:#fff; }
    .btn.primary:hover { background:#047857; }
    .status { font-size:0.8rem; color:#9ca3af; margin:10px 0; text-align:center; }
    .offline { margin-top:48px; text-align:center; color:#9ca3af; }
    .offline .big { font-size:3rem; }
    .offline .msg { margin:14px 0 20px; font-size:1.05rem; color:#d1d5db; }
    .dot { display:inline-block; width:8px; height:8px; border-radius:50%%; margin-right:6px; vertical-align:middle; }
    .dot.live { background:#34d399; } .dot.off { background:#6b7280; }
  </style>
</head>
<body>
  <div class="topbar">
    <a class="back" href="/">&larr; SportsHub</a>
    <div class="title"><span id="dot" class="dot off"></span><span id="camTitle">%s</span></div>
  </div>
  <div class="wrap">
    <div id="status" class="status">Connecting to SportsHub&hellip;</div>

    <div id="player" style="display:none; width:100%%; flex-direction:column; align-items:center;">
      <video id="video" autoplay controls playsinline muted></video>
      <div class="meta">WebRTC (low-latency): port %d<br>HLS: %s<br>RTMP (for OBS/VLC): %s</div>
      <button class="btn" onclick="(function(v){if(v.requestFullscreen)v.requestFullscreen();else if(v.webkitRequestFullscreen)v.webkitRequestFullscreen();})(document.getElementById('video'))">Fullscreen</button>
    </div>

    <div id="offline" class="offline" style="display:none">
      <div class="big">&#128247;</div>
      <div class="msg" id="offlineMsg">This camera isn&rsquo;t streaming right now.</div>
      <a class="btn primary" href="/">Open SportsHub to start it</a>
    </div>
  </div>

  <script src="/static/hls.min.js"></script>
  <script>
    const path = '%s';
    const webrtcUrl = 'http://%s:%d/' + path + '/whep';
    const hlsUrl = '%s';

    const video = document.getElementById('video');
    const statusEl = document.getElementById('status');
    const playerEl = document.getElementById('player');
    const offlineEl = document.getElementById('offline');
    const offlineMsg = document.getElementById('offlineMsg');
    const camTitle = document.getElementById('camTitle');
    const dot = document.getElementById('dot');

    let live = false;            // is the camera for this path currently live (per SSE)?
    let playerStarted = false;   // have we kicked off WebRTC/HLS for the current live session?
    let usingHls = false;
    let playing = false;
    let pc = null, hls = null;

    video.addEventListener('playing', () => { playing = true; });

    function showPlayer() { playerEl.style.display = 'flex'; offlineEl.style.display = 'none'; dot.className = 'dot live'; }
    function showOffline(msg) {
      stopPlayback();
      playerEl.style.display = 'none';
      offlineEl.style.display = 'block';
      dot.className = 'dot off';
      offlineMsg.textContent = msg || 'This camera isn’t streaming right now.';
      statusEl.textContent = '';
    }

    function stopPlayback() {
      playerStarted = false; usingHls = false; playing = false;
      try { if (pc) pc.close(); } catch (e) {}
      pc = null;
      try { if (hls) hls.destroy(); } catch (e) {}
      hls = null;
      video.srcObject = null;
      video.removeAttribute('src');
      try { video.load(); } catch (e) {}
    }

    function useHLS(reason) {
      if (usingHls || !live) return;
      usingHls = true;
      statusEl.textContent = 'Playing via HLS' + (reason ? ' (' + reason + ')' : '');
      try { if (pc) pc.close(); } catch (e) {}
      pc = null;
      video.srcObject = null;
      if (typeof Hls !== 'undefined' && Hls.isSupported()) {
        hls = new Hls({ enableWorker: true, lowLatencyMode: true, backBufferLength: 8, maxBufferLength: 12 });
        hls.loadSource(hlsUrl);
        hls.attachMedia(video);
        hls.on(Hls.Events.MANIFEST_PARSED, () => { video.play().catch(()=>{}); });
        hls.on(Hls.Events.ERROR, (e, d) => { if (d && d.fatal) statusEl.textContent = 'HLS error: ' + d.details; });
      } else if (video.canPlayType('application/vnd.apple.mpegurl')) {
        video.src = hlsUrl;
        video.addEventListener('loadedmetadata', () => { video.play().catch(()=>{}); });
      } else {
        video.src = hlsUrl;
      }
    }

    async function startWebRTC() {
      try {
        pc = new RTCPeerConnection();
        const myPc = pc;
        pc.ontrack = (event) => {
          if (myPc !== pc) return;
          if (event.streams && event.streams[0]) {
            video.srcObject = event.streams[0];
            video.play().catch(() => {});
          }
        };
        pc.onconnectionstatechange = () => {
          if (myPc !== pc || usingHls || !live) return;
          if (pc.connectionState === 'connected') {
            statusEl.textContent = 'Playing via WebRTC (low latency)';
          } else if (['failed', 'disconnected', 'closed'].includes(pc.connectionState)) {
            useHLS('WebRTC ' + pc.connectionState);
          } else {
            statusEl.textContent = 'WebRTC: ' + pc.connectionState;
          }
        };
        const offer = await pc.createOffer({ offerToReceiveVideo: true, offerToReceiveAudio: true });
        if (myPc !== pc) return;
        await pc.setLocalDescription(offer);
        const res = await fetch(webrtcUrl, { method: 'POST', headers: { 'Content-Type': 'application/sdp' }, body: offer.sdp });
        if (!res.ok) throw new Error('WHEP failed: ' + res.status);
        const answerSdp = await res.text();
        if (myPc !== pc) return;
        await pc.setRemoteDescription({ type: 'answer', sdp: answerSdp });
      } catch (err) {
        console.error('WebRTC failed, falling back to HLS:', err);
        useHLS('WebRTC unavailable');
      }
    }

    function startPlayback() {
      if (playerStarted) return;
      playerStarted = true;
      showPlayer();
      statusEl.textContent = 'Connecting (WebRTC for low latency)…';
      startWebRTC();
      // Watchdog: if no frames within a few seconds, fall back to HLS.
      setTimeout(() => { if (live && !playing && !usingHls) useHLS('WebRTC timeout'); }, 4500);
    }

    const isGcPath = /gc$/.test(path);

    function applySnapshot(snap) {
      const devs = (snap && Array.isArray(snap.devices)) ? snap.devices : [];
      // A device exposes its capture path only while live (state==live). It also exposes an
      // egressPath while pushing to GameChanger — the local copy of exactly that feed. Either
      // match means this path is streamable now.
      let dev = devs.find(d => d.path === path && d.state === 'live');
      let isGC = false;
      if (!dev) { dev = devs.find(d => d.egressPath === path && d.gcPhase === 'streaming'); isGC = !!dev; }
      if (dev) camTitle.textContent = (isGC ? 'GameChanger feed' : (dev.name || path)) + ' (' + path + ')';
      const nowLive = !!dev;
      if (nowLive && !live) { live = true; startPlayback(); }
      else if (!nowLive && live) { live = false; showOffline(isGcPath ? 'GameChanger stream has ended.' : 'Camera stopped. The stream has ended.'); }
      else if (!nowLive && !live && !playerStarted) { showOffline(isGcPath ? 'GameChanger isn’t streaming right now.' : null); }
    }

    function connectSSE() {
      const es = new EventSource('/api/events');
      es.onmessage = (e) => {
        let snap; try { snap = JSON.parse(e.data); } catch (_) { return; }
        applySnapshot(snap);
      };
      es.onerror = () => {
        if (!live) statusEl.textContent = 'Reconnecting to SportsHub…';
        // EventSource auto-reconnects.
      };
    }
    connectSSE();
  </script>
</body>
</html>`, streamPath, streamPath, webrtcPort, hlsURL, rtmpURL, streamPath, host, webrtcPort, hlsURL)
}
