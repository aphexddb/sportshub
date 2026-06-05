package app

import "fmt"

// watchPage renders the low-latency viewer page: it tries WebRTC (WHEP) first for ~1s
// latency on the LAN and falls back to (LL-)HLS. Behaviour mirrors the original page.
func watchPage(streamPath, host string, webrtcPort int, hlsURL, rtmpURL string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Sportshub • %s</title>
  <style>
    body { font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background:#0a0a0a; color:#eee; margin:0; padding:16px; display:flex; flex-direction:column; align-items:center; }
    h1 { font-size:1.1rem; margin:0 0 12px; }
    video { width:100%%; max-width:960px; background:#000; border-radius:12px; box-shadow:0 10px 30px rgba(0,0,0,0.6); }
    .meta { margin-top:12px; font-size:0.75rem; color:#666; word-break:break-all; text-align:center; }
    .btn { margin-top:12px; padding:10px 18px; background:#222; color:#ddd; border:1px solid #333; border-radius:8px; cursor:pointer; }
    .btn:hover { background:#333; }
    .status { font-size:0.8rem; color:#0f0; margin:8px 0; }
  </style>
</head>
<body>
  <h1>Live • %s</h1>
  <div id="status" class="status">Connecting (WebRTC for low latency)...</div>
  <video id="video" autoplay controls playsinline muted></video>
  <div class="meta">WebRTC (low-latency): port %d<br>HLS (higher latency): %s<br>RTMP (for OBS/VLC): %s</div>
  <button class="btn" onclick="const v=document.getElementById('video'); if(v.requestFullscreen) v.requestFullscreen(); else if(v.webkitRequestFullscreen) v.webkitRequestFullscreen();">Fullscreen</button>

  <script src="/static/hls.min.js"></script>
  <script>
    const video = document.getElementById('video');
    const statusEl = document.getElementById('status');
    const path = '%s';
    const webrtcUrl = 'http://%s:%d/' + path + '/whep';
    const hlsUrl = '%s';

    let usingHls = false;
    let playing = false;
    video.addEventListener('playing', () => { playing = true; });

    // Fall back to HLS. Idempotent: safe to call from the WHEP catch, a failed/disconnected
    // WebRTC connection, or the watchdog timer below.
    function useHLS(reason) {
      if (usingHls) return;
      usingHls = true;
      statusEl.textContent = 'Playing via HLS' + (reason ? ' (' + reason + ')' : '');
      try { if (window.__pc) window.__pc.close(); } catch (e) {}
      video.srcObject = null;
      if (typeof Hls !== 'undefined' && Hls.isSupported()) {
        const hls = new Hls({ enableWorker: true, lowLatencyMode: true, backBufferLength: 8, maxBufferLength: 12 });
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
        const pc = new RTCPeerConnection();
        window.__pc = pc;
        pc.ontrack = (event) => {
          if (event.streams && event.streams[0]) {
            video.srcObject = event.streams[0];
            video.play().catch(() => {});
          }
        };
        pc.onconnectionstatechange = () => {
          if (usingHls) return;
          if (pc.connectionState === 'connected') {
            statusEl.textContent = 'Playing via WebRTC (low latency)';
          } else if (['failed', 'disconnected', 'closed'].includes(pc.connectionState)) {
            useHLS('WebRTC ' + pc.connectionState);
          } else {
            statusEl.textContent = 'WebRTC: ' + pc.connectionState;
          }
        };

        const offer = await pc.createOffer({ offerToReceiveVideo: true, offerToReceiveAudio: true });
        await pc.setLocalDescription(offer);

        const res = await fetch(webrtcUrl, {
          method: 'POST',
          headers: { 'Content-Type': 'application/sdp' },
          body: offer.sdp
        });
        if (!res.ok) throw new Error('WHEP failed: ' + res.status);

        const answerSdp = await res.text();
        await pc.setRemoteDescription({ type: 'answer', sdp: answerSdp });
      } catch (err) {
        console.error('WebRTC failed, falling back to HLS:', err);
        useHLS('WebRTC unavailable');
      }
    }

    // Prefer WebRTC for ~1s latency on local LAN, but if no frames are playing within a few
    // seconds (e.g. WebRTC connected but ICE/codec produced no video), fall back to HLS so the
    // viewer always shows video.
    startWebRTC();
    setTimeout(() => { if (!playing && !usingHls) useHLS('WebRTC timeout'); }, 4500);
  </script>
</body>
</html>`, streamPath, streamPath, webrtcPort, hlsURL, rtmpURL, streamPath, host, webrtcPort, hlsURL)
}
