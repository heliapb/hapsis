#!/bin/sh
# sends one trace split across two tempos: root + child on eu, a second child on us
# hapsis should return all three spans.
set -eu

TRACE_ID=0123456789abcdef0123456789abcdef
NOW=$(date +%s)000000000
END=$(( $(date +%s) + 1 ))000000000

send() {
  until curl -sf "http://$1:3200/ready" >/dev/null; do sleep 1; done
  curl -sf -X POST "http://$1:4318/v1/traces" -H 'Content-Type: application/json' -d "{
    \"resourceSpans\": [{
      \"resource\": {\"attributes\": [{\"key\": \"service.name\", \"value\": {\"stringValue\": \"$2\"}}]},
      \"scopeSpans\": [{\"spans\": [$3]}]
    }]
  }" >/dev/null
}

span() {
  echo "{\"traceId\": \"$TRACE_ID\", \"spanId\": \"$1\", \"parentSpanId\": \"$2\", \"name\": \"$3\", \"kind\": 1, \"startTimeUnixNano\": \"$NOW\", \"endTimeUnixNano\": \"$END\"}"
}

send tempo-eu frontend "$(span 0000000000000001 '' 'GET /checkout'),$(span 0000000000000002 0000000000000001 'eu: load cart')"
send tempo-us payments "$(span 0000000000000003 0000000000000001 'us: charge card')"

echo "seeded trace $TRACE_ID"
