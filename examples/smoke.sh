#!/usr/bin/env bash
#
# smoke.sh — end-to-end smoke test for a running layerid-edge instance.
#
# Usage:
#   # In one terminal:
#   LAYERID_EDGE_AUTH_DISABLED=1 go run ./cmd/layerid-edge
#
#   # In another:
#   ./examples/smoke.sh
#
# What it does, in order:
#   1. GET /healthz                  — server is up
#   2. GET /v1/scorers               — list the available scorers
#   3. POST /v1/score   (const-deny) — verifies the const scorer
#   4. POST /v1/init    (weighted)   — assigns a req_id, scores upfront
#   5. GET  /v1/lookup/<req_id>      — replay the verdict
#   6. POST /v1/consume/<req_id>     — first call: replayed=false
#   7. POST /v1/consume/<req_id>     — same id: replayed=true (idempotent)
#
# All paths exit non-zero on the first unexpected response so CI can
# wire this in directly.

set -euo pipefail

BASE_URL="${LAYERID_EDGE_BASE_URL:-http://localhost:8080}"

# In auth-disabled mode the server injects a synthetic tenant_0 claim,
# so the Authorization header is ignored. We send a placeholder anyway
# so this script works identically against a real-auth deploy when the
# user exports LAYERID_EDGE_TOKEN.
TOKEN="${LAYERID_EDGE_TOKEN:-dev-no-auth}"

step() { printf '\n\033[1;36m▶ %s\033[0m\n' "$1"; }
ok()   { printf '  \033[1;32m✓\033[0m %s\n' "$1"; }
fail() { printf '  \033[1;31m✗\033[0m %s\n' "$1"; exit 1; }

# ---------- 1. healthz ------------------------------------------------------
step "healthz"
HEALTH="$(curl -fsS "${BASE_URL}/healthz")"
echo "  ${HEALTH}"
echo "${HEALTH}" | grep -q '"ok":true' || fail "healthz did not return ok:true"
ok "server is up"

# ---------- 2. list scorers -------------------------------------------------
step "list scorers"
SCORERS="$(curl -fsS "${BASE_URL}/v1/scorers")"
echo "  ${SCORERS}"
echo "${SCORERS}" | grep -q '"weighted"' || fail "weighted scorer missing"
ok "default scorer registered"

# ---------- 3. const-deny scorer (no signals required) ----------------------
step "const-deny (proves /v1/score works with no signals)"
CONST_RESP="$(curl -fsS -X POST "${BASE_URL}/v1/score" \
    -H 'Content-Type: application/json' \
    -H "Authorization: Bearer ${TOKEN}" \
    -d '{"scorer":"const-deny","signals":{}}')"
echo "  ${CONST_RESP}"
echo "${CONST_RESP}" | grep -q '"verdict":"deny"' || fail "const-deny didn't return deny"
ok "const-deny returns deny"

# ---------- 4. /v1/init with a populated signal payload ---------------------
step "init with a populated 7-signal payload (weighted scorer)"

# Build a "looks like a real browser" payload that exercises all seven of
# the currently-implemented signals: ip_match, timings, screen_resolution,
# window_screen_ratio, webdriver, timezone, webgl_renderer.
SIGNALS='{
  "IP": "1.2.3.4",
  "Webdriver": false,
  "TZ": "Europe/Berlin",
  "ScreenW": 1920,
  "ScreenH": 1080,
  "WindowW": 1600,
  "WindowH": 900,
  "WebGLVendor": "Apple Inc.",
  "AppMs": 220
}'

INIT_RESP="$(curl -fsS -X POST "${BASE_URL}/v1/init" \
    -H 'Content-Type: application/json' \
    -H "Authorization: Bearer ${TOKEN}" \
    -d "{\"signals\": ${SIGNALS}}")"
echo "  ${INIT_RESP}"
echo "${INIT_RESP}" | grep -q '"insufficient_data":true' && \
    fail "weighted scorer returned insufficient_data — fewer than 6 signals fired"

REQ_ID="$(printf '%s' "${INIT_RESP}" | sed -n 's/.*"req_id":"\([^"]*\)".*/\1/p')"
VERDICT="$(printf '%s' "${INIT_RESP}" | sed -n 's/.*"verdict":"\([^"]*\)".*/\1/p')"
[ -n "${REQ_ID}" ] || fail "no req_id in init response"
ok "got req_id=${REQ_ID} verdict=${VERDICT}"

# ---------- 5. /v1/lookup/<req_id> ------------------------------------------
step "lookup"
LOOKUP="$(curl -fsS "${BASE_URL}/v1/lookup/${REQ_ID}" \
    -H "Authorization: Bearer ${TOKEN}")"
echo "  ${LOOKUP}"
echo "${LOOKUP}" | grep -q "\"req_id\":\"${REQ_ID}\"" || fail "lookup didn't return req_id"
ok "lookup matches"

# ---------- 6. /v1/consume/<req_id> first time ------------------------------
step "consume (first time)"
CONSUME1="$(curl -fsS -X POST "${BASE_URL}/v1/consume/${REQ_ID}" \
    -H "Authorization: Bearer ${TOKEN}")"
echo "  ${CONSUME1}"
echo "${CONSUME1}" | grep -q '"replayed":false' || fail "first consume should not be replayed"
ok "first consume: replayed=false"

# ---------- 7. /v1/consume/<req_id> idempotent ------------------------------
step "consume (same req_id, should replay)"
CONSUME2="$(curl -fsS -X POST "${BASE_URL}/v1/consume/${REQ_ID}" \
    -H "Authorization: Bearer ${TOKEN}")"
echo "  ${CONSUME2}"
echo "${CONSUME2}" | grep -q '"replayed":true' || fail "second consume should be replayed"
ok "second consume: replayed=true (idempotent)"

printf '\n\033[1;32mAll green. End-to-end flow works.\033[0m\n'
