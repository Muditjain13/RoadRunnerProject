# Kubernetes manifests

Plain per-resource YAML (no Kustomize/Helm layer) so a reviewer can open
any one file and see exactly what's deployed - consistent with this
repo's general bias toward explicit code over abstraction: "readable by
a senior engineer reviewing it in 15 minutes."

## Layout

- `00-namespace.yaml`, `01-secrets.yaml` - shared setup
- `10`-`14` - infrastructure (MySQL, MongoDB, Redis, Kafka/Redpanda, Jaeger), single-replica
- `20`-`27` - the 8 application services, numbered to match `docker-compose.yml`'s dependency order

## Apply

```bash
kubectl apply -f deploy/k8s/
```

Files are numbered so a flat `kubectl apply -f deploy/k8s/` applies
infra before the services that depend on it, and readiness probes cover
the rest of the ordering (a service's pod won't pass readiness until its
own dependencies are reachable, so a `Recreate`-strategy race isn't a
correctness problem here even without `depends_on`-style sequencing,
which Kubernetes doesn't have natively).

## What's honest about this, and what isn't verified

This was written directly, not applied against a running cluster - there
is no Kubernetes control plane in the sandbox this repo was built in.
Concretely, that means:

- **Not verified**: that these manifests actually `kubectl apply` cleanly
  end-to-end, that the probes' timing (`initialDelaySeconds`, etc.) is
  well-tuned for a real cold start, or that resource requests/limits are
  right for real load. They're written to be *correct as YAML* and to
  reflect the real service topology (ports, env vars, and dependencies
  all come directly from `docker-compose.yml` and each
  `cmd/<service>/main.go`), not load-tested.
- **No image registry wired up**: nothing builds and pushes
  `transport-app/<service>:latest` yet - see the comment in
  `20-catalog.yaml`. A real deployment needs a CI step for that.
- **No Ingress/TLS**: `gateway`'s Service is `type: LoadBalancer` for a
  minimal "how do I reach this" story. A real deployment would more
  likely use `ClusterIP` + an Ingress controller.
- **Single-replica infra**: MySQL, MongoDB, Redis, and Kafka all run as
  one pod with no failover - deliberate, since replicated data stores are
  a different (and much larger) engineering problem than this repo is
  about. A real deployment uses managed services or dedicated operators
  for these.

What *is* real: every port, environment variable, and inter-service
dependency here is read directly from the actual running code
(`cmd/*/main.go`'s `envOr()` defaults) and from `docker-compose.yml`,
which the app has actually been run under in this repo's development
history - not invented for this file.
