#!/usr/bin/env bash
# End-to-end smoke test against a running `docker compose up` stack.
# Exercises every major feature with the synthetic sample videos and prints a summary.
# Requires: curl, jq, node (>= 22, for the WebSocket probe).
set -euo pipefail

API=${API:-http://localhost:20901}
INF=${INF:-http://localhost:20902}
UI=${UI:-http://localhost:20900}
PROM=${PROM:-http://localhost:20920}
GRAFANA=${GRAFANA:-http://localhost:20921}
TIMEOUT=${TIMEOUT:-150}

pass=0; fail=0
ok()   { printf '  \033[32mPASS\033[0m %s\n' "$*"; pass=$((pass + 1)); }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$*"; fail=$((fail + 1)); }
step() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
check() { if eval "$2"; then ok "$1"; else bad "$1"; fi; }

wait_for() { # url, label
  local i
  for i in $(seq 1 90); do
    if curl -fsS -o /dev/null "$1"; then ok "$2 reachable"; return 0; fi
    sleep 2
  done
  bad "$2 not reachable at $1"; return 1
}

api() { # method path token [json]
  local method=$1 path=$2 token=${3:-} body=${4:-}
  local args=(-sS -X "$method" -H 'Content-Type: application/json' -w '\n%{http_code}')
  [[ -n $token ]] && args+=(-H "Authorization: Bearer $token")
  [[ -n $body ]] && args+=(-d "$body")
  curl "${args[@]}" "$API$path"
}
code() { tail -n1 <<<"$1"; }
json() { sed '$d' <<<"$1"; }

login() {
  local body resp
  body=$(jq -cn --arg e "$1" --arg p "$2" '{email: $e, password: $p}')
  resp=$(api POST /api/v1/auth/login '' "$body")
  json "$resp" | jq -r .access_token
}

step "Health"
wait_for "$API/readyz" "API /readyz"
wait_for "$INF/health" "inference /health"
wait_for "$UI/login" "frontend"
wait_for "$PROM/-/ready" "Prometheus"
wait_for "$GRAFANA/api/health" "Grafana"

step "Auth & RBAC"
ADMIN=$(login admin@visionguard.dev admin12345)
OP=$(login operator@visionguard.dev operator12345)
VIEWER=$(login viewer@visionguard.dev viewer12345)
check "seeded users can log in" '[[ ${#ADMIN} -gt 20 && ${#OP} -gt 20 && ${#VIEWER} -gt 20 ]]'
r=$(api POST /api/v1/cameras "$VIEWER" '{"name":"x","source_type":"rtsp","source_uri":"rtsp://x/y"}')
check "viewer cannot create cameras (403)" '[[ $(code "$r") == 403 ]]'
r=$(api GET /api/v1/users "$OP")
check "operator cannot list users (403)" '[[ $(code "$r") == 403 ]]'
r=$(api POST /api/v1/cameras "$OP" '{"name":"bad","source_type":"rtsp","source_uri":"ftp://nope"}')
check "invalid RTSP URL rejected (422)" '[[ $(code "$r") == 422 ]]'

step "Cameras & live inference"
CAMS=$(json "$(api GET /api/v1/cameras "$VIEWER")")
WH=$(jq -r '.[] | select(.name=="Warehouse Floor (sample)") | .id' <<<"$CAMS")
ST=$(jq -r '.[] | select(.name=="Street Crossing (sample)") | .id' <<<"$CAMS")
check "seeded sample cameras present" '[[ -n $WH && -n $ST ]]'
fps=0
for _ in $(seq 1 45); do
  fps=$(json "$(api GET "/api/v1/cameras/$WH" "$VIEWER")" | jq -r '.status.fps // 0')
  state=$(json "$(api GET "/api/v1/cameras/$WH" "$VIEWER")" | jq -r .status.state)
  [[ $state == online ]] && awk "BEGIN{exit !($fps > 0)}" && break
  sleep 2
done
check "warehouse camera online (fps=$fps)" '[[ $state == online ]]'
snap_bytes=$(curl -fsS "$API/api/v1/cameras/$WH/snapshot?access_token=$VIEWER" | wc -c | tr -d ' ')
check "live snapshot JPEG ($snap_bytes bytes)" '[[ $snap_bytes -gt 5000 ]]'
mjpeg=$(curl -sS --max-time 2 "$API/api/v1/cameras/$WH/stream?access_token=$VIEWER" 2>/dev/null | LC_ALL=C tr -d "\\000" | LC_ALL=C grep -a -c 'image/jpeg' || true)
check "MJPEG stream delivers frames" '[[ $mjpeg -ge 1 ]]'

WS_URL="${API/http/ws}/api/v1/ws?access_token=$VIEWER"
ws_out=$(node -e '
const [url, cam] = process.argv.slice(1);
const ws = new WebSocket(url); let frames = 0, tracks = 0, first = 0, last = 0, lat = 0;
ws.onopen = () => ws.send(JSON.stringify({type: "subscribe", camera_id: cam}));
ws.onmessage = (e) => { const m = JSON.parse(e.data); if (m.type !== "frame") return;
  frames++; tracks += m.data.tracks.length; lat += m.data.latency_ms; const t = Date.parse(m.data.ts);
  if (!first) first = t; last = t; };
setTimeout(() => { const secs = (last - first) / 1000;
  console.log(JSON.stringify({frames, fps: secs > 0 ? +((frames - 1) / secs).toFixed(2) : 0,
    avg_tracks: frames ? +(tracks / frames).toFixed(2) : 0, avg_latency_ms: frames ? +(lat / frames).toFixed(2) : 0}));
  process.exit(0); }, 5000);
ws.onerror = (e) => { console.log(JSON.stringify({error: String(e.message || e)})); process.exit(0); };
' "$WS_URL" "$WH")
WS_FPS=$(jq -r .fps <<<"$ws_out"); WS_LAT=$(jq -r .avg_latency_ms <<<"$ws_out")
check "WebSocket frames with tracks: $ws_out" '[[ $(jq -r .frames <<<"$ws_out") -gt 10 ]] && awk "BEGIN{exit !($(jq -r .avg_tracks <<<"$ws_out") > 0)}"'

step "Rules fire -> incidents (waiting up to ${TIMEOUT}s for one video loop)"
want="zone_intrusion missing_ppe dwell_time crowd_count"
deadline=$((SECONDS + TIMEOUT))
while :; do
  INC=$(json "$(api GET "/api/v1/incidents?camera_id=$WH&page_size=100&from=$(date -u -v-30M +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '-30 min' +%Y-%m-%dT%H:%M:%SZ)" "$VIEWER")")
  have=$(jq -r '[.items[] | select(.description_provider != null or true) | .rule_type] | unique | join(" ")' <<<"$INC")
  missing=0; for w in $want; do [[ " $have " == *" $w "* ]] || missing=1; done
  (( missing == 0 || SECONDS > deadline )) && break
  sleep 5
done
for w in $want; do check "rule type '$w' fired" '[[ " $have " == *" $w "* ]]'; done
LIVE=$(jq '[.items[] | select(.snapshot_url != null)]' <<<"$INC")
N_ALERTS=$(jq length <<<"$LIVE")
check "live incidents with snapshots: $N_ALERTS" '[[ $N_ALERTS -ge 4 ]]'
FIRST=$(jq -r '.[-1].id' <<<"$LIVE")
for _ in $(seq 1 20); do
  DETAIL=$(json "$(api GET "/api/v1/incidents/$FIRST" "$VIEWER")")
  [[ $(jq -r '.clip_url // empty' <<<"$DETAIL") != "" && $(jq -r '.description // empty' <<<"$DETAIL") != "" ]] && break
  sleep 2
done
check "AI description: $(jq -r '.description_provider' <<<"$DETAIL") — \"$(jq -r '.description' <<<"$DETAIL" | cut -c1-80)...\"" '[[ -n $(jq -r ".description // empty" <<<"$DETAIL") ]]'
SNAP_URL=$(jq -r .snapshot_url <<<"$DETAIL"); CLIP_URL=$(jq -r .clip_url <<<"$DETAIL")
check "snapshot downloadable" '[[ $(curl -s -o /dev/null -w "%{http_code}" "$API$SNAP_URL?access_token=$VIEWER") == 200 ]]'
clip_type=$(curl -s -o /dev/null -w '%{content_type}' "$API$CLIP_URL?access_token=$VIEWER")
check "event clip downloadable ($clip_type)" '[[ $clip_type == video/mp4 ]]'
# Cooldown/de-dup: intrusion fires at most once per 20 s cooldown window per loop.
intr=$(jq '[.[] | select(.rule_type=="zone_intrusion")] | length' <<<"$LIVE")
check "no alert storm (zone_intrusion count=$intr over the run)" '[[ $intr -le 8 ]]'
r=$(api PATCH "/api/v1/incidents/$FIRST" "$OP" '{"status":"acknowledged"}')
check "operator acknowledges incident" '[[ $(code "$r") == 200 ]]'
r=$(api PATCH "/api/v1/incidents/$FIRST" "$VIEWER" '{"status":"resolved"}')
check "viewer cannot resolve incident (403)" '[[ $(code "$r") == 403 ]]'
r=$(api GET "/api/v1/incidents?q=helmet&page_size=5" "$VIEWER")
check "full-text incident search (helmet: $(json "$r" | jq .total) hits)" '[[ $(json "$r" | jq .total) -ge 1 ]]'

step "Zone editor + new rule on the street camera"
r=$(api POST "/api/v1/cameras/$ST/zones" "$OP" '{"name":"Self-crossing","kind":"polygon","points":[[0,0],[1,1],[1,0],[0,1]]}')
check "self-intersecting polygon rejected (422)" '[[ $(code "$r") == 422 ]]'
r=$(api POST "/api/v1/cameras/$ST/zones" "$OP" '{"name":"Whole street","kind":"polygon","points":[[0,0],[1,0],[1,1],[0,1]],"color":"#a855f7"}')
ZONE=$(json "$r" | jq -r .id)
check "polygon zone created" '[[ $(code "$r") == 201 ]]'
body=$(jq -cn --arg z "$ZONE" '{name: "Smoke crowd", type: "crowd_count", zone_id: $z, params: {max_count: 0, classes: ["car"], cooldown_seconds: 5}, severity: "low"}')
r=$(api POST "/api/v1/cameras/$ST/rules" "$OP" "$body")
check "crowd rule created" '[[ $(code "$r") == 201 ]]'
got=0
for _ in $(seq 1 30); do
  got=$(json "$(api GET "/api/v1/incidents?camera_id=$ST&rule_type=crowd_count" "$VIEWER")" | jq .total)
  [[ $got -ge 1 ]] && break; sleep 2
done
check "new rule fired live within 60s ($got incidents)" '[[ $got -ge 1 ]]'

step "Upload a video as a new camera"
tmpv=$(mktemp -t vgclip.XXXX).mp4
docker cp visionguard-inference:/data/videos/samples/street.mp4 "$tmpv" >/dev/null 2>&1 || true
if [[ -s $tmpv ]]; then
  r=$(curl -sS -w '\n%{http_code}' -H "Authorization: Bearer $OP" -F "file=@$tmpv;type=video/mp4" -F name="Uploaded clip" "$API/api/v1/cameras/upload")
  UP=$(json "$r" | jq -r .id)
  check "upload accepted (201)" '[[ $(code "$r") == 201 ]]'
  for _ in $(seq 1 20); do
    st=$(json "$(api GET "/api/v1/cameras/$UP" "$VIEWER")" | jq -r .status.state); [[ $st == online ]] && break; sleep 2
  done
  check "uploaded camera online" '[[ $st == online ]]'
  r=$(api DELETE "/api/v1/cameras/$UP" "$OP"); check "camera deleted" '[[ $(code "$r") == 204 ]]'
else
  bad "could not copy sample video for upload"
fi
rm -f "$tmpv"

step "Analytics"
SUM=$(json "$(api GET /api/v1/analytics/summary "$VIEWER")")
check "summary: $(jq -c . <<<"$SUM")" '[[ $(jq .cameras_online <<<"$SUM") -ge 2 ]]'
r=$(json "$(api GET "/api/v1/analytics/counts?camera_id=$WH&bucket=1m" "$VIEWER")")
check "people/vehicle counts over time ($(jq length <<<"$r") buckets)" '[[ $(jq length <<<"$r") -gt 0 ]]'
r=$(json "$(api GET "/api/v1/analytics/heatmap?camera_id=$WH" "$VIEWER")")
check "movement heatmap (max cell=$(jq .max <<<"$r"))" '[[ $(jq .max <<<"$r") -gt 0 ]]'
r=$(json "$(api GET "/api/v1/analytics/line-crossings?camera_id=$WH&bucket=5m" "$VIEWER")")
CROSS=$(jq -c 'group_by(.direction) | map({(.[0].direction): (map(.count) | add)}) | add' <<<"$r")
check "line-crossing counts in/out: $CROSS" '[[ $(jq length <<<"$r") -gt 0 ]]'

step "Model management"
MODELS=$(json "$(api GET /api/v1/models "$VIEWER")")
check "model catalogue ($(jq length <<<"$MODELS") models)" '[[ $(jq length <<<"$MODELS") -ge 3 ]]'
ONNX_FPS="n/a"
if [[ $(jq -r '.[] | select(.id=="yolo11n-onnx") | .available' <<<"$MODELS") == true ]]; then
  r=$(api POST /api/v1/models/yolo11n-onnx/activate "$ADMIN"); check "admin activates YOLO11n ONNX" '[[ $(code "$r") == 200 ]]'
  for _ in $(seq 1 20); do
    m=$(curl -fsS "$INF/pipelines" | jq -r --arg id "$WH" '.[] | select(.camera_id==$id) | .model'); [[ $m == yolo11n-onnx ]] && break; sleep 1
  done
  sleep 8
  ONNX_FPS=$(curl -fsS "$INF/pipelines" | jq -r --arg id "$WH" '.[] | select(.camera_id==$id) | "\(.fps) fps, \(.latency_ms) ms/frame"')
  check "pipeline hot-swapped to $m ($ONNX_FPS)" '[[ $m == yolo11n-onnx ]]'
  api POST /api/v1/models/color-synthetic/activate "$ADMIN" >/dev/null
else
  ok "YOLO11n ONNX weights not mounted; activation skipped"
fi
r=$(api POST /api/v1/models/yolo11n/activate "$ADMIN")
check "unavailable model activation refused (409: PyTorch not in CPU image)" '[[ $(code "$r") == 409 ]]'

step "Metrics"
targets=$(curl -fsS "$PROM/api/v1/targets" | jq -r '[.data.activeTargets[] | select(.health=="up")] | length')
check "Prometheus targets up: $targets" '[[ $targets -ge 3 ]]'
inf_fps=$(curl -fsS "$PROM/api/v1/query?query=avg(vg_inference_fps)" | jq -r '.data.result[0].value[1] // "0"')
check "vg_inference_fps in Prometheus ($inf_fps)" 'awk "BEGIN{exit !($inf_fps > 0)}"'
dash=$(curl -fsS "$GRAFANA/api/search?query=VisionGuard" | jq length)
check "Grafana dashboard provisioned" '[[ $dash -ge 1 ]]'

printf '\n\033[1mSummary\033[0m  passed=%d failed=%d  ws_fps=%s  ws_latency_ms=%s  live_alerts=%s  onnx=%s\n' \
  "$pass" "$fail" "$WS_FPS" "$WS_LAT" "$N_ALERTS" "$ONNX_FPS"
[[ $fail -eq 0 ]]
