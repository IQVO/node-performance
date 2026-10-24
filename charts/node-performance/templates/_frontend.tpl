{{/*
Fully qualified name of the frontend Module Federation remote deployment/service.

The remote (web/, `node_performance_mfe`) is served by its own nginx pod and
reached through the Nginx web gateway at /mfes/node-performance/. It is
deliberately a separate workload from the API: the Gateway API route never
points at it, and the api Service must never select it (component=frontend
vs component=api). Kept in its own helper file so the frontend component
stays self-contained (templates/frontend-*.yaml + this file + the frontend:
values block).
*/}}
{{- define "node-performance.frontendFullname" -}}
{{- include "node-performance.fullname" . }}-frontend
{{- end }}
