#!/usr/bin/env bash
# Chart render assertions for node-performance: Postgres and Kafka are the only
# runtime, so the mandatory configuration has no silent default, no *_MODE is
# ever rendered, and the env cmd/api reads is exactly the env the chart renders.
# Run: bash charts/node-performance/tests/render_test.sh   (needs helm)
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_dir="$(cd "$chart_dir/../.." && pwd)"
release=node-performance
command -v helm >/dev/null || { echo "FAIL: helm is not installed" >&2; exit 1; }

# Dummy values, no password: nothing here is ever dialled.
groups=(vendorConnectivityConsumerGroup:node-performance-vendor-connectivity
  shippingAndTrackingConsumerGroup:node-performance-shipping-and-tracking
  deliveryPromiseConsumerGroup:node-performance-delivery-promise
  offerAvailabilityConsumerGroup:node-performance-offer-availability
  nodeTopologyConsumerGroup:node-performance-node-topology
  sellerInboundConsumerGroup:node-performance-seller-inbound)
base=(--set database.url=postgres://u@example.invalid:5432/db
  --set kafka.brokers=kafka.example.invalid:9092)
for g in "${groups[@]}"; do base+=(--set "config.${g%%:*}=${g#*:}"); done

fail() { echo "FAIL: $*" >&2; exit 1; }
tpl() { helm template "$release" "$chart_dir" "$@"; }

configmap="$(tpl "${base[@]}" --show-only templates/configmap.yaml)"
deployment="$(tpl "${base[@]}" --show-only templates/deployment.yaml)"
all="$(tpl "${base[@]}")"
configmap_name="$(awk '/^  name:/ {print $2; exit}' <<<"$configmap")"
[[ -n "$configmap_name" ]] || fail "could not read the ConfigMap name"

env_value() { # env_value <rendered> <NAME>
  grep -A1 "name: $2\$" <<<"$1" | grep -m1 'value:' | sed -E 's/.*value: "?//; s/"$//'
}

# --- the ConfigMap is mounted (envFrom) ---------------------------------------------
grep -A3 'envFrom:' <<<"$deployment" | grep -q 'configMapRef:' || fail "the api Deployment does not mount the ConfigMap via envFrom"
grep -A3 'envFrom:' <<<"$deployment" | grep -q "name: $configmap_name" || fail "the api Deployment envFrom does not reference ConfigMap $configmap_name"
grep -q 'checksum/config:' <<<"$deployment" || fail "the api Deployment lacks the checksum/config annotation"

# --- chart vs binary: every env var cmd/api reads is rendered ------------------------
# The names come from the binary's own constants (cmd/api/config.go and the
# Postgres adapter's config.go), so a new variable fails this test until the
# chart renders it.
# shellcheck disable=SC2207
api_vars=($({ grep -ohE '^[[:space:]]*env[A-Za-z]+[[:space:]]*=[[:space:]]*"[A-Z][A-Z0-9_]*"' "$repo_dir/cmd/api/config.go";
  grep -ohE '^[[:space:]]*Env[A-Za-z]+[[:space:]]*=[[:space:]]*"[A-Z][A-Z0-9_]*"' "$repo_dir/internal/adapters/outbound/postgres/config.go"; } \
  | sed -E 's/.*"([A-Z0-9_]+)"/\1/' | sort -u))
(( ${#api_vars[@]} >= 14 )) || fail "found only ${#api_vars[@]} env constants in cmd/api/config.go; the scan is looking in the wrong place"
for var in "${api_vars[@]}"; do
  grep -q "name: $var\$" <<<"$deployment" || fail "the api Deployment does not render $var, which cmd/api reads"
  case "$var" in
    DATABASE_URL|MIGRATIONS_DATABASE_URL) ;; # secretKeyRef, never in the ConfigMap
    *) grep -q "^  $var: " <<<"$configmap" || fail "the ConfigMap does not carry $var, which cmd/api reads" ;;
  esac
done
# The named list of the brief, spelled out so a rename in both places cannot pass silently.
for var in DATABASE_URL MIGRATIONS_DATABASE_URL KAFKA_BROKERS VENDOR_CONNECTIVITY_CONSUMER_GROUP \
  SHIPPING_AND_TRACKING_CONSUMER_GROUP DELIVERY_PROMISE_CONSUMER_GROUP OFFER_AVAILABILITY_CONSUMER_GROUP \
  NODE_TOPOLOGY_CONSUMER_GROUP SELLER_INBOUND_CONSUMER_GROUP HTTP_ADDR RECOMPUTE_INTERVAL OUTBOX_RELAY_INTERVAL OUTBOX_MAX_ATTEMPTS \
  SHUTDOWN_DRAIN_DELAY LOG_LEVEL; do
  [[ " ${api_vars[*]} " == *" $var "* ]] || fail "$var is expected to be read by cmd/api but the scan did not find it"
done
# No dead keys: every ConfigMap key is a variable cmd/api reads.
while read -r key; do
  [[ " ${api_vars[*]} " == *" $key "* ]] || fail "ConfigMap key $key is read by no binary"
done < <(awk '/^data:/ {d=1; next} d && /^  [A-Z]/ {sub(/:.*/, ""); sub(/^  /, ""); print}' <<<"$configmap")
# No env var is set twice on the container.
dups="$(grep -E '^ {12}- name: ' <<<"$deployment" | sort | uniq -d)"
[[ -z "$dups" ]] || fail "the api Deployment renders an env var twice: $dups"

# --- rendered from values ---------------------------------------------------------------
[[ "$(env_value "$deployment" KAFKA_BROKERS)" == "kafka.example.invalid:9092" ]] || fail "KAFKA_BROKERS is not rendered from kafka.brokers"
for g in "${groups[@]}"; do
  case "${g%%:*}" in
    vendorConnectivityConsumerGroup) var=VENDOR_CONNECTIVITY_CONSUMER_GROUP ;;
    shippingAndTrackingConsumerGroup) var=SHIPPING_AND_TRACKING_CONSUMER_GROUP ;;
    deliveryPromiseConsumerGroup) var=DELIVERY_PROMISE_CONSUMER_GROUP ;;
    offerAvailabilityConsumerGroup) var=OFFER_AVAILABILITY_CONSUMER_GROUP ;;
    nodeTopologyConsumerGroup) var=NODE_TOPOLOGY_CONSUMER_GROUP ;;
    *) var=SELLER_INBOUND_CONSUMER_GROUP ;;
  esac
  [[ "$(env_value "$deployment" "$var")" == "${g#*:}" ]] || fail "$var is not rendered from config.${g%%:*}"
  grep -q "^  $var: \"${g#*:}\"" <<<"$configmap" || fail "the ConfigMap does not carry $var=${g#*:}"
done
tuned="$(tpl "${base[@]}" --set config.recomputeInterval=7m --set config.outboxRelayInterval=7s --set config.outboxMaxAttempts=5 --set config.logLevel=debug --show-only templates/configmap.yaml)"
grep -q 'RECOMPUTE_INTERVAL: "7m"' <<<"$tuned" || fail "config.recomputeInterval is not rendered"
grep -q 'OUTBOX_RELAY_INTERVAL: "7s"' <<<"$tuned" || fail "config.outboxRelayInterval is not rendered"
grep -q 'OUTBOX_MAX_ATTEMPTS: "5"' <<<"$tuned" || fail "config.outboxMaxAttempts is not rendered"
grep -q 'LOG_LEVEL: "debug"' <<<"$tuned" || fail "config.logLevel is not rendered"
grep -q 'OUTBOX_MAX_ATTEMPTS: "20"' <<<"$configmap" || fail "OUTBOX_MAX_ATTEMPTS must default to 20 (ADR 0005)"
unlimited="$(tpl "${base[@]}" --set config.outboxMaxAttempts=0 --show-only templates/configmap.yaml)"
grep -q 'OUTBOX_MAX_ATTEMPTS: "0"' <<<"$unlimited" || fail "config.outboxMaxAttempts=0 (unlimited) is not rendered"

# --- DATABASE_URL: a Secret, never the ConfigMap ----------------------------------------
grep -A4 'name: DATABASE_URL$' <<<"$deployment" | grep -q 'secretKeyRef:' || fail "DATABASE_URL must come from a Secret (secretKeyRef)"
if grep -qE '^  (MIGRATIONS_)?DATABASE_URL:' <<<"$configmap"; then fail "a DSN must never land in the ConfigMap"; fi
grep -A6 'name: MIGRATIONS_DATABASE_URL$' <<<"$deployment" | grep -q 'optional: true' || fail "MIGRATIONS_DATABASE_URL must be an optional secretKeyRef"
grep -A6 'name: MIGRATIONS_DATABASE_URL$' <<<"$deployment" | grep -q 'key: MIGRATIONS_DATABASE_URL' || fail "MIGRATIONS_DATABASE_URL must read database.migrationsExistingSecretKey"
existing="$(tpl --set database.existingSecret=my-db --set kafka.brokers=k:9092 $(printf -- '--set config.%s ' "${groups[@]/:/=}") --show-only templates/deployment.yaml)"
grep -A4 'name: DATABASE_URL$' <<<"$existing" | grep -q 'name: my-db' || fail "database.existingSecret is not the DATABASE_URL secret"
if grep -q 'kind: Secret' <<<"$(tpl --set database.existingSecret=my-db --set kafka.brokers=k:9092 $(printf -- '--set config.%s ' "${groups[@]/:/=}"))"; then
  fail "no Secret may be created when database.existingSecret is set"
fi
no_migrations="$(tpl "${base[@]}" --set database.migrationsExistingSecretKey= --show-only templates/deployment.yaml)"
if grep -q 'name: MIGRATIONS_DATABASE_URL' <<<"$no_migrations"; then fail "an empty migrationsExistingSecretKey must not render MIGRATIONS_DATABASE_URL"; fi

# --- pod hardening, probes, shutdown room ----------------------------------------------
for needle in 'runAsNonRoot: true' 'readOnlyRootFilesystem: true' 'allowPrivilegeEscalation: false' 'type: RuntimeDefault'; do
  grep -q "$needle" <<<"$deployment" || fail "the api Deployment lacks '$needle'"
done
grep -A2 'drop:' <<<"$deployment" | grep -q 'ALL' || fail "the api container must drop ALL capabilities"
grep -A3 'livenessProbe:' <<<"$deployment" | grep -q 'path: /healthz' || fail "livenessProbe must hit /healthz"
grep -A3 'readinessProbe:' <<<"$deployment" | grep -q 'path: /readyz' || fail "readinessProbe must hit /readyz"
grep -q 'terminationGracePeriodSeconds: 40' <<<"$deployment" || fail "terminationGracePeriodSeconds must default to 40 (> 5+10+10+10)"
grep -q 'ghcr.io/iqvo/node-performance:v0.1.0' <<<"$deployment" || fail "the default image must be ghcr.io/iqvo/node-performance:v<appVersion>"
chart_name="$(awk '/^name:/ {print $2}' "$chart_dir/Chart.yaml")"
[[ "$chart_name" == "node-performance" && "$(basename "$chart_dir")" == "node-performance" ]] || fail "chart directory and Chart.yaml name must both be node-performance"

# --- components: only the api; optional extras are off by default --------------------------
if grep -qE 'kind: (HorizontalPodAutoscaler|Ingress|HTTPRoute)' <<<"$all"; then fail "HPA, Ingress and HTTPRoute must be off by default"; fi
[[ "$(grep -c '^kind: Deployment' <<<"$all")" == 1 ]] || fail "exactly one Deployment (the api) must render"
if grep -qiE 'projector|reports|frontend|mcp' <<<"$all"; then fail "mcp and frontend must be off by default; no projector or reports component belongs in this chart"; fi
if grep -qiE 'value: "?(permissive|log|off)"?$|in-memory|permissive' <<<"$all"$'\n'"$(cat "$chart_dir/templates/NOTES.txt")"; then
  fail "the render or NOTES.txt carries a permissive/log/off or in-memory mode"
fi
if grep -qE 'name: [A-Z_]*_MODE$' <<<"$all"; then fail "no *_MODE variable may be rendered"; fi
extras="$(tpl "${base[@]}" --set autoscaling.api.enabled=true --set ingress.enabled=true --set gatewayApi.enabled=true --set gatewayApi.parentRefs[0].name=gw --set gatewayApi.parentRefs[0].namespace=ns)"
for k in HorizontalPodAutoscaler Ingress HTTPRoute; do grep -q "kind: $k" <<<"$extras" || fail "$k did not render when enabled"; done

# --- observability (ADR 0010, fleet standard item 7) -------------------------------------
# The api renders the fleet pipeline's env: the Collector endpoint, the service
# name, the version (image tag, appVersion when empty) and the environment.
otel_endpoint="otel-collector.observability.svc.cluster.local:4317"
[[ "$(env_value "$deployment" OTEL_EXPORTER_OTLP_ENDPOINT)" == "$otel_endpoint" ]] \
  || fail "the api Deployment does not render OTEL_EXPORTER_OTLP_ENDPOINT from otel.endpoint"
[[ "$(env_value "$deployment" OTEL_SERVICE_NAME)" == "node-performance" ]] \
  || fail "the api Deployment does not render OTEL_SERVICE_NAME=node-performance from otel.serviceName"
[[ "$(env_value "$deployment" SERVICE_VERSION)" == "0.1.0" ]] || fail "SERVICE_VERSION must default to the chart appVersion"
[[ "$(env_value "$deployment" ENVIRONMENT)" == "production" ]] || fail "ENVIRONMENT must come from environment"
tagged="$(tpl "${base[@]}" --set image.tag=v9.9.9 --set otel.serviceName=np --set otel.endpoint=http://collector:4317 \
  --set environment=staging --show-only templates/deployment.yaml)"
[[ "$(env_value "$tagged" SERVICE_VERSION)" == "v9.9.9" ]] || fail "SERVICE_VERSION must follow image.tag"
[[ "$(env_value "$tagged" OTEL_SERVICE_NAME)" == "np" ]] || fail "OTEL_SERVICE_NAME must follow otel.serviceName"
[[ "$(env_value "$tagged" OTEL_EXPORTER_OTLP_ENDPOINT)" == "http://collector:4317" ]] || fail "OTEL_EXPORTER_OTLP_ENDPOINT must follow otel.endpoint"
[[ "$(env_value "$tagged" ENVIRONMENT)" == "staging" ]] || fail "ENVIRONMENT must follow environment"
# otel.enabled=false renders no OTEL_* env (SERVICE_VERSION stays: it also labels the logs' build).
otel_off="$(tpl "${base[@]}" --set otel.enabled=false --set mcp.enabled=true)"
if grep -q 'name: OTEL_' <<<"$otel_off"; then fail "otel.enabled=false must render no OTEL_* env in any component"; fi
grep -q 'name: SERVICE_VERSION$' <<<"$otel_off" || fail "SERVICE_VERSION must still render with otel.enabled=false"
# The values are chart-owned: extraEnv may not set them a second time.
for owned in OTEL_SERVICE_NAME OTEL_EXPORTER_OTLP_ENDPOINT SERVICE_VERSION ENVIRONMENT; do
  err="$(tpl "${base[@]}" --set "extraEnv[0].name=$owned" --set 'extraEnv[0].value=x' 2>&1)" \
    && fail "extraEnv must not be able to set $owned"
  grep -qF "extraEnv sets $owned" <<<"$err" || fail "the refusal for extraEnv $owned does not name it: $err"
done

# --- refusals: the mandatory values name themselves ------------------------------------
expect_refusal() { # expect_refusal <why> <needle> <helm args...>
  local why="$1" needle="$2" err; shift 2
  if err="$(tpl "$@" 2>&1)"; then fail "the chart must refuse to render: $why"; fi
  grep -qF -- "$needle" <<<"$err" || fail "the refusal for '$why' does not name '$needle':
$err"
}
mandatory_except() { # mandatory_except <database|kafka|<group key>>
  local skip="$1" out=()
  [[ "$skip" == database ]] || out+=(--set database.url=postgres://u@h/db)
  [[ "$skip" == kafka ]] || out+=(--set kafka.brokers=k:9092)
  for g in "${groups[@]}"; do
    [[ "${g%%:*}" == "$skip" ]] || out+=(--set "config.${g%%:*}=${g#*:}")
  done
  printf '%s\n' "${out[@]}"
}
refusal_without() { # refusal_without <value to leave out> <needle>
  local args=() line
  while IFS= read -r line; do args+=("$line"); done < <(mandatory_except "$1")
  expect_refusal "no $1" "$2" "${args[@]}"
}
refusal_without database "database.url or database.existingSecret"
refusal_without kafka "kafka.brokers"
for g in "${groups[@]}"; do refusal_without "${g%%:*}" "config.${g%%:*}"; done
expect_refusal "a pod-name derived group" "config.deliveryPromiseConsumerGroup" "${base[@]}" --set 'config.deliveryPromiseConsumerGroup=np-$(POD_NAME)'
expect_refusal "a *_MODE through extraEnv" "_MODE" "${base[@]}" --set 'extraEnv[0].name=DEMAND_MODE' --set 'extraEnv[0].value=permissive'
expect_refusal "a consumer group overridden through extraEnv" "NODE_TOPOLOGY_CONSUMER_GROUP" "${base[@]}" --set 'extraEnv[0].name=NODE_TOPOLOGY_CONSUMER_GROUP' --set 'extraEnv[0].value=x'
expect_refusal "a grace period under the shutdown bounds" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=39
expect_refusal "a grace period of 35s (not above the 35s sum)" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=35
expect_refusal "no grace period" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=0
expect_refusal "a longer drain delay without more grace" "terminationGracePeriodSeconds" "${base[@]}" --set config.shutdownDrainDelay=30s
tpl "${base[@]}" --set terminationGracePeriodSeconds=40 >/dev/null || fail "a 40s grace period must render"
tpl "${base[@]}" --set config.shutdownDrainDelay=30s --set terminationGracePeriodSeconds=65 >/dev/null || fail "a 30s drain with a 65s grace period must render"
expect_refusal "an unparsable drain delay" "config.shutdownDrainDelay" "${base[@]}" --set config.shutdownDrainDelay=soon
expect_refusal "a listen port that is not the container port" "config.httpAddr" "${base[@]}" --set config.httpAddr=:9090


# --- optional components (ADR 0007 mcp, ADR 0008 console remote) ----------------------
# render_one <template> <extra helm args...>: one template, the mandatory base set.
render_one() { helm template "$release" "$chart_dir" --show-only "templates/$1" "${@:2}"; }
# binary_env_of <main.go>: the upper-case env literals a cmd reads.
binary_env_of() { grep -ohE '"[A-Z][A-Z0-9_]*"' "$@" | tr -d '"' | sort -u; }
assert_env() { # assert_env <rendered> <what> <VAR>...
  local rendered="$1" what="$2"; shift 2
  for var in "$@"; do grep -q "name: $var$" <<<"$rendered" || fail "$what does not render $var"; done
}

# mcp: its own env, never the api ConfigMap, never Kafka (ADR 0007).
mcp_dep="$(render_one mcp-deployment.yaml "${base[@]}" --set mcp.enabled=true)"
grep -q 'app.kubernetes.io/component: mcp' <<<"$mcp_dep" || fail "the mcp Deployment was not rendered with mcp.enabled=true"
grep -q 'command: \["/app/mcp"\]' <<<"$mcp_dep" || fail "the mcp Deployment must run /app/mcp"
if grep -q 'configMapRef:' <<<"$mcp_dep"; then fail "mcp-deployment.yaml must not mount the api ConfigMap"; fi
if grep -Eq 'name: (KAFKA_BROKERS|OUTBOX_[A-Z_]+|RECOMPUTE_[A-Z_]+|[A-Z]+_CONSUMER_GROUP)$' <<<"$mcp_dep"; then
  fail "the mcp Deployment must not get Kafka/relay/scheduler/consumer env (read-only, never dials Kafka)"
fi
if grep -qE 'name: [A-Z_]*_MODE$' <<<"$mcp_dep"; then fail "no *_MODE variable may be rendered on the mcp Deployment"; fi
# shellcheck disable=SC2207
mcp_vars=($(binary_env_of "$repo_dir/cmd/mcp/main.go" "$repo_dir/cmd/mcp/telemetry.go"))
(( ${#mcp_vars[@]} >= 5 )) || fail "found only ${#mcp_vars[@]} env literals in cmd/mcp/main.go; the scan is looking in the wrong place"
assert_env "$mcp_dep" "the mcp Deployment" "${mcp_vars[@]}" OTEL_SERVICE_NAME OTEL_EXPORTER_OTLP_ENDPOINT
[[ "$(env_value "$mcp_dep" SERVICE_VERSION)" == "0.1.0" ]] || fail "the mcp SERVICE_VERSION must default to the image tag / appVersion, like the api"
[[ "$(env_value "$mcp_dep" OTEL_SERVICE_NAME)" == "node-performance-mcp" ]] || fail "the mcp component must export as <otel.serviceName>-mcp"
[[ "$(env_value "$mcp_dep" OTEL_EXPORTER_OTLP_ENDPOINT)" == "$otel_endpoint" ]] || fail "the mcp component must export to otel.endpoint"
mcp_versioned="$(render_one mcp-deployment.yaml "${base[@]}" --set mcp.enabled=true --set mcp.serviceVersion=dev)"
[[ "$(env_value "$mcp_versioned" SERVICE_VERSION)" == "dev" ]] || fail "SERVICE_VERSION must come from mcp.serviceVersion when set"
mcp_named="$(render_one mcp-deployment.yaml "${base[@]}" --set mcp.enabled=true --set otel.serviceName=np)"
[[ "$(env_value "$mcp_named" OTEL_SERVICE_NAME)" == "np-mcp" ]] || fail "the mcp service name must follow otel.serviceName"
grep -A4 'name: DATABASE_URL$' <<<"$mcp_dep" | grep -q 'secretKeyRef:' || fail "the mcp Deployment's DATABASE_URL must come from a Secret"
grep -A6 'name: MIGRATIONS_DATABASE_URL$' <<<"$mcp_dep" | grep -q 'optional: true' || fail "the mcp Deployment's MIGRATIONS_DATABASE_URL must be an optional secretKeyRef"
dups="$(grep -E '^ {12}- name: ' <<<"$mcp_dep" | sort | uniq -d)"
[[ -z "$dups" ]] || fail "the mcp Deployment renders an env var twice: $dups"

mcp_svc="$(render_one mcp-service.yaml "${base[@]}" --set mcp.enabled=true)"
grep -q 'app.kubernetes.io/component: mcp' <<<"$mcp_svc" || fail "the mcp Service does not pin component=mcp"
grep -q 'type: ClusterIP' <<<"$mcp_svc" || fail "the mcp Service must stay ClusterIP"

# The host route rewrites /api/mcp/node-performance to the SDK's /mcp endpoint.
mcp_route="$(render_one mcp-route.yaml "${base[@]}" --set mcp.enabled=true --set mcp.route.enabled=true \
  --set mcp.route.parentRefs[0].name=gw --set mcp.route.parentRefs[0].namespace=kong)"
grep -q 'kind: HTTPRoute' <<<"$mcp_route" || fail "the mcp HTTPRoute was not rendered with mcp.route.enabled=true"
grep -q 'value: /api/mcp/node-performance' <<<"$mcp_route" || fail "the mcp route must carry the fleet host path"
grep -q 'replacePrefixMatch: /mcp' <<<"$mcp_route" || fail "the mcp route must rewrite the prefix to /mcp"
grep -q "name: ${release}-mcp" <<<"$mcp_route" || fail "the mcp route must target the mcp Service"

# frontend: the web remote (ADR 0008). Static bytes; no database, no Kafka.
frontend_dep="$(render_one frontend-deployment.yaml "${base[@]}" --set frontend.enabled=true)"
frontend_svc="$(render_one frontend-service.yaml "${base[@]}" --set frontend.enabled=true)"
grep -q 'app.kubernetes.io/component: frontend' <<<"$frontend_dep" || fail "the frontend Deployment was not rendered with frontend.enabled=true"
grep -q 'type: ClusterIP' <<<"$frontend_svc" || fail "the frontend Service must stay ClusterIP"
if grep -Eq 'name: (DATABASE_URL|MIGRATIONS_DATABASE_URL|KAFKA_BROKERS)$' <<<"$frontend_dep"; then fail "the frontend Deployment must not get database/Kafka env"; fi
if grep -q 'command:' <<<"$frontend_dep"; then fail "the frontend container runs the image's own nginx entrypoint"; fi

# All three components at once: every Service still selects exactly one
# Deployment (tests/test_service_selectors.py proves it structurally).
together="$(tpl "${base[@]}" --set mcp.enabled=true --set frontend.enabled=true)"
[[ "$(grep -c '^kind: Deployment$' <<<"$together")" == 3 ]] || fail "api + mcp + frontend must render three Deployments"
[[ "$(grep -c '^kind: Service$' <<<"$together")" == 3 ]] || fail "api + mcp + frontend must render three Services"
if grep -E 'kind: (Ingress|HTTPRoute)' <<<"$together" | grep -q frontend; then
  fail "frontend routing must not live in this chart"
fi
# ...and the ci values file used by ct lint renders the same set.
ci_all="$(helm template "$release" "$chart_dir" -f "$chart_dir/ci/all-components-values.yaml")"
for needle in 'component: mcp' 'component: frontend' 'command: ["/app/mcp"]'; do
  grep -qF "$needle" <<<"$ci_all" || fail "ci/all-components-values.yaml must render $needle"
done

echo "PASS: node-performance chart render assertions"
