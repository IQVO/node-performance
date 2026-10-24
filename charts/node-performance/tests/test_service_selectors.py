#!/usr/bin/env python3
"""Assert this chart's Service selectors isolate each component.

Why this exists (fleet-wide): `app.kubernetes.io/name` + `instance` are
identical on every pod a release creates. A Service that selects on those two
alone selects ALL of them. Every Deployment here carries
`app.kubernetes.io/component` in selector.matchLabels and its pod labels, and
every Service selects on it, so each Service selects EXACTLY ONE Deployment.
This test fails if that ever stops being true.

This chart has one component, api (a single Deployment and its Service); HPA,
Ingress and HTTPRoute are optional and off by default. The test renders it
with those enabled and with the defaults.

Run: python3 charts/node-performance/tests/test_service_selectors.py
Needs: helm, PyYAML.
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

CHART_DIR = Path(__file__).resolve().parents[1]
RELEASE = "node-performance"

# Dummy values, no password: nothing here is ever dialled.
GROUPS = {
    "vendorConnectivityConsumerGroup": "node-performance-vendor-connectivity",
    "shippingAndTrackingConsumerGroup": "node-performance-shipping-and-tracking",
    "deliveryPromiseConsumerGroup": "node-performance-delivery-promise",
    "offerAvailabilityConsumerGroup": "node-performance-offer-availability",
    "nodeTopologyConsumerGroup": "node-performance-node-topology",
    "sellerInboundConsumerGroup": "node-performance-seller-inbound",
}


def runtime(skip: str | None = None) -> list[str]:
    """The mandatory values, minus the one named by skip (database, kafka or a group key)."""
    args: list[str] = []
    if skip != "database":
        args += ["--set", "database.url=postgres://u@example.invalid:5432/db"]
    if skip != "kafka":
        args += ["--set", "kafka.brokers=kafka.example.invalid:9092"]
    for key, value in GROUPS.items():
        if skip != key:
            args += ["--set", f"config.{key}={value}"]
    return args


BASE = runtime()
ENABLE_EVERYTHING = BASE + [
    "--set", "autoscaling.api.enabled=true",
    "--set", "ingress.enabled=true",
    "--set", "gatewayApi.enabled=true",
    "--set", "gatewayApi.parentRefs[0].name=gw",
    "--set", "gatewayApi.parentRefs[0].namespace=infra",
]

# Every env var cmd/api reads.
API_ENV = (
    "DATABASE_URL", "MIGRATIONS_DATABASE_URL", "KAFKA_BROKERS",
    "VENDOR_CONNECTIVITY_CONSUMER_GROUP", "SHIPPING_AND_TRACKING_CONSUMER_GROUP",
    "DELIVERY_PROMISE_CONSUMER_GROUP", "OFFER_AVAILABILITY_CONSUMER_GROUP", "NODE_TOPOLOGY_CONSUMER_GROUP",
    "SELLER_INBOUND_CONSUMER_GROUP",
    "HTTP_ADDR", "RECOMPUTE_INTERVAL", "OUTBOX_RELAY_INTERVAL", "OUTBOX_MAX_ATTEMPTS",
    "SHUTDOWN_DRAIN_DELAY", "LOG_LEVEL",
)


def helm_template(extra_args: list[str]) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["helm", "template", RELEASE, str(CHART_DIR), *extra_args],
        capture_output=True, text=True,
    )


def render(extra_args: list[str]) -> list[dict]:
    result = helm_template(extra_args)
    if result.returncode != 0:
        raise SystemExit(f"FAIL: helm template {' '.join(extra_args)}:\n{result.stderr}")
    try:
        import yaml  # type: ignore
    except ModuleNotFoundError:  # pragma: no cover - environment guard
        print("SKIP: PyYAML not available; cannot assert selectors", file=sys.stderr)
        raise SystemExit(0)
    return [d for d in yaml.safe_load_all(result.stdout) if d]


def selector_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("selector") or {}


def pod_labels_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("template", {}).get("metadata", {}).get("labels") or {}


def matches(selector: dict, labels: dict) -> bool:
    return bool(selector) and all(labels.get(k) == v for k, v in selector.items())


def env_names(dep: dict) -> list[str]:
    return [e["name"] for e in dep["spec"]["template"]["spec"]["containers"][0].get("env", [])]


def check_components(docs: list[dict], failures: list[str]) -> None:
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}

    if sorted(deployments) != [RELEASE]:
        failures.append(f"only the api Deployment may render, got {sorted(deployments)}")
    if sorted(services) != [RELEASE]:
        failures.append(f"only the api Service may render, got {sorted(services)}")
    if RELEASE not in services:
        failures.append("the api Service was not rendered")
    elif selector_of(services[RELEASE]).get("app.kubernetes.io/component") != "api":
        failures.append("the api Service selector must pin component=api")

    # Every Deployment's own selector must pin a component too, and be
    # satisfied by its pod labels.
    for name, dep in deployments.items():
        match_labels = dep["spec"]["selector"].get("matchLabels") or {}
        if "app.kubernetes.io/component" not in match_labels:
            failures.append(f"Deployment {name} selector.matchLabels lacks app.kubernetes.io/component")
        if not matches(match_labels, pod_labels_of(dep)):
            failures.append(f"Deployment {name} pod labels do not satisfy its own selector")

    # The real invariant: each Service selects exactly one Deployment.
    for svc_name, svc in services.items():
        sel = selector_of(svc)
        hit = [d for d, dep in deployments.items() if matches(sel, pod_labels_of(dep))]
        if len(hit) != 1:
            failures.append(f"Service {svc_name} selects {len(hit)} Deployments {sorted(hit)}; expected exactly 1")

    # The Service port must reach the container port named http.
    api = deployments.get(RELEASE)
    if api is None:
        failures.append("the api Deployment was not rendered")
        return
    ports = api["spec"]["template"]["spec"]["containers"][0].get("ports", [])
    if not any(p.get("name") == "http" for p in ports):
        failures.append("the api container must expose a port named http (the Service targets it)")

    api_env = env_names(api)
    for want in API_ENV:
        if want not in api_env:
            failures.append(f"the api Deployment does not render {want}")
    duplicates = sorted({n for n in api_env if api_env.count(n) > 1})
    if duplicates:
        failures.append(f"the api Deployment renders env vars twice: {duplicates}")
    modes = [n for n in api_env if n.endswith("_MODE")]
    if modes:
        failures.append(f"no *_MODE variable may be rendered: {modes}")


def check_optional(docs: list[dict], failures: list[str]) -> None:
    kinds = {d.get("kind") for d in docs}
    for kind in ("HorizontalPodAutoscaler", "Ingress", "HTTPRoute"):
        if kind not in kinds:
            failures.append(f"{kind} was not rendered when enabled")
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}
    hpa = next((d for d in docs if d.get("kind") == "HorizontalPodAutoscaler"), None)
    if hpa is not None and hpa["spec"]["scaleTargetRef"]["name"] != RELEASE:
        failures.append("the HPA does not scale the api Deployment")
    if "replicas" in deployments.get(RELEASE, {}).get("spec", {}):
        failures.append("the api Deployment must omit replicas when its HPA owns them")


def check_refusals(failures: list[str]) -> None:
    cases: list[tuple[str, list[str], str]] = [
        ("without a database source", runtime("database"), "requires database.url or database.existingSecret"),
        ("without kafka.brokers", runtime("kafka"), "kafka.brokers"),
        ("a *_MODE through extraEnv",
         BASE + ["--set", "extraEnv[0].name=DEMAND_MODE", "--set", "extraEnv[0].value=permissive"], "extraEnv sets DEMAND_MODE"),
        ("a grace period under 40s", BASE + ["--set", "terminationGracePeriodSeconds=39"], "terminationGracePeriodSeconds"),
    ]
    for key in GROUPS:
        cases.append((f"without config.{key}", runtime(key), f"config.{key} is required"))
    for label, args, needle in cases:
        result = helm_template(args)
        if result.returncode == 0 or needle not in result.stderr:
            failures.append(f"chart rendered (or failed for another reason) {label}")


def main() -> int:
    failures: list[str] = []

    check_components(render(ENABLE_EVERYTHING), failures)
    check_optional(render(ENABLE_EVERYTHING), failures)

    # Default values must not deploy an HPA, an Ingress or a route.
    defaults = render(BASE)
    check_components(defaults, failures)
    stray = [d["kind"] for d in defaults if d.get("kind") in {"HorizontalPodAutoscaler", "Ingress", "HTTPRoute"}]
    if stray:
        failures.append(f"optional components rendered with default values: {stray}")

    check_refusals(failures)

    if failures:
        for f in failures:
            print(f"FAIL: {f}")
        return 1

    print("PASS: the api Service selects exactly one Deployment (the api); HPA, Ingress and HTTPRoute are off by default "
          "and render when enabled; every env var cmd/api reads is rendered and no *_MODE is; the chart refuses to render "
          "without a database source, brokers or any of the six consumer groups, with a *_MODE in extraEnv, or with a "
          "grace period under 40s")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
