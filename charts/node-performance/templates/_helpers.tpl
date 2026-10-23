{{/*
Expand the name of the chart.
*/}}
{{- define "node-performance.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "node-performance.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Chart name and version as used by the chart label.
*/}}
{{- define "node-performance.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "node-performance.labels" -}}
helm.sh/chart: {{ include "node-performance.chart" . }}
{{ include "node-performance.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels. Every Deployment and Service ALSO pins
app.kubernetes.io/component (api): a Service selecting on these two alone
would select every component a future change adds.
*/}}
{{- define "node-performance.selectorLabels" -}}
app.kubernetes.io/name: {{ include "node-performance.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "node-performance.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "node-performance.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Name of the Secret holding DATABASE_URL: the operator's own
(database.existingSecret) or the one this chart creates from database.url.
*/}}
{{- define "node-performance.databaseSecretName" -}}
{{- if .Values.database.existingSecret }}
{{- .Values.database.existingSecret }}
{{- else }}
{{- include "node-performance.fullname" . }}-database
{{- end }}
{{- end }}

{{/*
Image reference. Empty image.tag = "v" + appVersion (the release job tags the
published image vX.Y.Z and packages the chart with --app-version X.Y.Z).
*/}}
{{- define "node-performance.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default (printf "v%s" .Chart.AppVersion) }}
{{- end }}

{{/*
MANDATORY: cmd/api exits at boot without DATABASE_URL; there is no in-memory
mode. Fails the render naming the values instead of crash-looping.
*/}}
{{- define "node-performance.requireDatabase" -}}
{{- if not (or .Values.database.url .Values.database.existingSecret) -}}
{{- fail "node-performance requires database.url or database.existingSecret to be set (DATABASE_URL): the service runs on real Postgres only and exits at boot without it." -}}
{{- end -}}
{{- end -}}

{{/*
MANDATORY: cmd/api exits at boot without KAFKA_BROKERS (relay, consumers and
the /readyz broker check). There is no default broker and no way to switch
Kafka off.
*/}}
{{- define "node-performance.requireKafka" -}}
{{- $_ := required "node-performance requires kafka.brokers to be set (KAFKA_BROKERS, comma-separated host:port): the service runs on real Kafka only and exits at boot without it." .Values.kafka.brokers -}}
{{- end -}}

{{/*
All five consumers always run, each under a STABLE consumer group id that must
come from values: cmd/api exits at boot without any of the five. A stable id
is what lets a rescheduled pod resume from its committed offsets; an id
derived from the pod name (an env expansion such as $(POD_NAME)) would not.
*/}}
{{- define "node-performance.requireConsumerGroups" -}}
{{- range $c := list
    (dict "key" "vendorConnectivityConsumerGroup" "env" "VENDOR_CONNECTIVITY_CONSUMER_GROUP")
    (dict "key" "shippingAndTrackingConsumerGroup" "env" "SHIPPING_AND_TRACKING_CONSUMER_GROUP")
    (dict "key" "deliveryPromiseConsumerGroup" "env" "DELIVERY_PROMISE_CONSUMER_GROUP")
    (dict "key" "offerAvailabilityConsumerGroup" "env" "OFFER_AVAILABILITY_CONSUMER_GROUP")
    (dict "key" "nodeTopologyConsumerGroup" "env" "NODE_TOPOLOGY_CONSUMER_GROUP") -}}
{{- $v := toString (index $.Values.config $c.key) -}}
{{- if or (eq $v "") (eq $v "<nil>") -}}
{{- fail (printf "config.%s is required (%s): the consumer always runs and cmd/api exits at boot without a group. Use a STABLE, service-prefixed id (for example node-performance-<topic>), never the pod name." $c.key $c.env) -}}
{{- end -}}
{{- if contains "$(" $v -}}
{{- fail (printf "config.%s is %q - a consumer group id must be a literal, stable value, never derived from the pod name or any env expansion (%s)." $c.key $v $c.env) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
extraEnv may not bring back an in-memory mode or override what the chart
renders: no *_MODE variable at all (this service has none; a mode switch would
be a way around the real runtime) and none of the variables the chart sets
from values (a consumer group overridden through extraEnv would bypass the
checks above).
*/}}
{{- define "node-performance.requireCleanExtraEnv" -}}
{{- $managed := list "DATABASE_URL" "MIGRATIONS_DATABASE_URL" "KAFKA_BROKERS" "HTTP_ADDR" "RECOMPUTE_INTERVAL" "OUTBOX_RELAY_INTERVAL" "OUTBOX_MAX_ATTEMPTS" "SHUTDOWN_DRAIN_DELAY" "LOG_LEVEL" "VENDOR_CONNECTIVITY_CONSUMER_GROUP" "SHIPPING_AND_TRACKING_CONSUMER_GROUP" "DELIVERY_PROMISE_CONSUMER_GROUP" "OFFER_AVAILABILITY_CONSUMER_GROUP" "NODE_TOPOLOGY_CONSUMER_GROUP" -}}
{{- range .Values.extraEnv -}}
{{- $name := toString .name -}}
{{- if hasSuffix "_MODE" $name -}}
{{- fail (printf "extraEnv sets %s - *_MODE variables are refused: node-performance runs on real Postgres and real Kafka only, with no permissive, log or off mode." $name) -}}
{{- end -}}
{{- if has $name $managed -}}
{{- fail (printf "extraEnv sets %s - the chart already renders it from values (config.*, database.*, kafka.brokers); set it there." $name) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
SHUTDOWN_DRAIN_DELAY in whole seconds ("5s" or "5"); anything else is refused
so the grace-period check below never guesses.
*/}}
{{- define "node-performance.drainSeconds" -}}
{{- $raw := toString .Values.config.shutdownDrainDelay -}}
{{- if not (regexMatch "^[0-9]+s?$" $raw) -}}
{{- fail (printf "config.shutdownDrainDelay is %q - want whole seconds such as \"5s\" (or \"0\" to disable)." $raw) -}}
{{- end -}}
{{- trimSuffix "s" $raw -}}
{{- end -}}

{{/*
The graceful shutdown is sequential: SHUTDOWN_DRAIN_DELAY + HTTP drain 10s +
consumers and scheduler 10s + relay 10s. The pod needs more than that sum; the
chart requires drain + 35 (5s of margin; 40 with the default 5s drain). A
shorter grace period SIGKILLs the consumers or the relay mid-drain and strands
an uncommitted offset or an in-flight outbox batch.
*/}}
{{- define "node-performance.requireGracePeriod" -}}
{{- $drain := int (include "node-performance.drainSeconds" .) -}}
{{- $want := add $drain 35 -}}
{{- if lt (int .Values.terminationGracePeriodSeconds) $want -}}
{{- fail (printf "terminationGracePeriodSeconds is %v - want at least %v (SHUTDOWN_DRAIN_DELAY %vs + HTTP 10s + consumers 10s + relay 10s + 5s margin): a shorter grace period SIGKILLs the consumers or the relay mid-drain." .Values.terminationGracePeriodSeconds $want $drain) -}}
{{- end -}}
{{- end -}}

{{/*
HTTP_ADDR must listen on the port the container, probes and Service target.
*/}}
{{- define "node-performance.requireHTTPPort" -}}
{{- if not (hasSuffix (printf ":%v" .Values.service.targetPort) (toString .Values.config.httpAddr)) -}}
{{- fail (printf "config.httpAddr is %q but service.targetPort is %v - the listen port must match the container port." .Values.config.httpAddr .Values.service.targetPort) -}}
{{- end -}}
{{- end -}}

{{/*
Every render-time requirement of the real runtime, in one include. The api
Deployment (always rendered) includes it, so a mis-set release fails
`helm template` / `helm install`.
*/}}
{{- define "node-performance.requireRealRuntime" -}}
{{- include "node-performance.requireDatabase" . -}}
{{- include "node-performance.requireKafka" . -}}
{{- include "node-performance.requireConsumerGroups" . -}}
{{- include "node-performance.requireCleanExtraEnv" . -}}
{{- include "node-performance.requireGracePeriod" . -}}
{{- include "node-performance.requireHTTPPort" . -}}
{{- end -}}
