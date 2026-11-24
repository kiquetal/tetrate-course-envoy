# Pyloros — Roadmap to v1.0

> **Pyloros** (πυλωρός): *the gatekeeper* — the one who decides who passes through the gate.
> A tenant-aware edge platform: **Go** compiles desired state into xDS,
> **Java 21** decides policy, **Envoy** carries the traffic.
>
> Pronounced *pee-lo-ROS*. Works as binary (`pyloros`), CLI (`pyloros apply`) and domain.

This is the execution plan that turns the Envoy course labs (`04-xds-dynamic-config/`)
into a released project. Every phase ends with something you can **demo and measure**,
not something you merely finished.

---

## 0. Naming and identity

| Item | Value |
| :--- | :--- |
| Project | **Pyloros** |
| Repo | `pyloros` (new repository; this course repo stays as notes) |
| CLI | `pyloros` (`pyloros apply`, `pyloros diff`, `pyloros rollback`) |
| Tagline | *Compile tenancy to xDS. Decide at the gate.* |
| Version plan | `v0.1` (P1) → `v0.2` (P2/P3) → `v1.0` (P5) |

The name is the thesis: the **gatekeeper** admits, denies and meters traffic; it does not
carry it. Envoy carries, Go compiles the rules, Java is the gatekeeper's judgment.

Alternates considered: Stoa (Greek portico), Krites (judge), Propylaea (the gateway),
Portico, Turnstile, Viaduct. Rejected for length, pronunciation, or name collision.

---

## 1. Repository layout (target)

```
pyloros/
├── README.md                 # 1 diagram, 3 numbers, 5-minute demo
├── Makefile                  # make up | down | smoke | load | chaos
├── docs/
│   ├── adr/0001-....md       # one decision per file
│   ├── slo.md                # targets + error budget policy
│   ├── runbook.md            # alarm → first 3 actions
│   ├── drills.md             # expected vs observed failure table
│   └── postmortems/          # at least one, blameless
├── control-plane/            # GO
│   ├── cmd/pyloros/          # CLI
│   ├── internal/compile/     # desired state → snapshot (pure, testable)
│   ├── internal/xds/         # ADS server, NACK callbacks, versions
│   └── internal/store/       # Postgres: tenants, routes, quotas, audit
├── policy/                   # JAVA 21
│   ├── ext-authz/            # Authorization.Check
│   └── rls/                  # ShouldRateLimit + Redis
├── services/                 # JAVA 21 demo workloads
├── envoy/
│   ├── bootstrap.json        # the ONLY static config
│   └── docker-compose.yaml
├── load/k6/                  # scenarios + results/
└── chaos/                    # scripts + expected outcomes
```

---

## 2. Phases

### P0 — Skeleton (week 1)

**Build**
* Empty repo, Makefile, Compose with Envoy + one backend.
* `docs/adr/0001-boundaries.md` (Go vs Java vs Envoy, one-way flows).
* CI: `go vet`, `go build`, `envoy --mode validate`, `docker compose config -q`, `make smoke`.

**Exit criteria**
* `make smoke` runs in CI on every push and asserts: traffic returns 200, Envoy restart count stays 0.
* A newcomer can run the repo from the README alone.

**Demo:** CI green + `make up && curl`.

---

### P1 — Control plane v1 (weeks 2–3)

**Build**
* Postgres schema: `tenant`, `route`, `upstream`, `quota`, `audit`.
* `internal/compile`: pure function `desired state → snapshot` (expand → link → dedupe → validate → version → diff).
* ADS server (CDS + RDS), per-type version tracking, ACK/NACK callbacks, `last_good` per node.
* `pyloros diff|apply|rollback`, snapshot persistence, audit log ("who/what/when/ticket").
* `/status`: served version, per-node ACKed version, `stale_clients`, `nack_total`.

**Exit criteria**
* New tenant published in **< 500 ms** with **0 dropped requests** (measured with k6 while applying).
* `pyloros apply --dry-run` prints the exact resources that would change.
* A deliberately broken reference is **rejected at compile**, nothing is published.
* `pyloros rollback <version>` restores service in < 1 s.

**Demo:** add a tenant live; break a config on purpose (see it rejected); roll back.

---

### P2 — L7 edge (weeks 3–4)

**Build**
* Replace `tcp_proxy` with `http_connection_manager`; RDS-driven vhosts per tenant.
* TLS termination with SDS (cert rotation without restart), `jwt_authn` for identity.
* Access logs (JSON), OpenTelemetry traces edge → service.
* Per-route timeouts and retry policy compiled from tenant spec.

**Exit criteria**
* Route change (path → new cluster) live with no restart.
* Certificate rotation with **0 failed requests**.
* One trace spans client → Envoy → service with the same request id.

**Demo:** rotate a cert and rename a route while k6 runs.

---

### P3 — Policy (weeks 5–6)

**Build**
* Java `ext_authz` service: tenant resolution (JWT/host), allow/deny, header injection, dynamic metadata.
* Java RLS + Redis: per-tenant token buckets, `x-ratelimit-*` headers, fail-open/fail-closed switch.
* Quota descriptors compiled into RDS `rate_limits`; `failure_mode_deny` documented per environment.

**Exit criteria**
* Quota changed 50 rps → 5 rps **live**, 429s appear, no restart, no code deploy.
* Suspended tenant denied in < 1 s from a DB update.
* Measured cost of the policy hop: p50/p99 delta, published in the README.

**Demo:** flip a quota and suspend a tenant while traffic runs.

---

### P4 — Resilience (week 6)

**Build**
* `outlier_detection`, `circuit_breakers`, retries + `retry_budget`, `per_try_timeout`.
* Graceful drain: endpoint removal before shutdown; `drain_time` on listeners.
* Nested-timeout budget enforced by the compiler (inner < outer, validated).

**Exit criteria**
* Kill 1 of 3 backends → **0 HTTP 5xx** observed.
* Kill the control plane → running Envoy unaffected; cold-start gap documented.
* A retry storm is capped by the retry budget (numbers shown).

**Demo:** `chaos/` script with before/after metrics.

---

### P5 — Release (weeks 7–8)

**Build**
* k6 report: p99 overhead, fan-out time, memory per Envoy at 10 / 100 / 1000 vhosts.
* `docs/drills.md`: expected vs observed for 6 failure scenarios.
* `docs/runbook.md` (5 alarms → first 3 actions), `docs/slo.md` (targets + error budget).
* One blameless postmortem of a simulated bad publish (NACK storm).
* 6–10 ADRs, each with the rejected alternative.
* Tag `v1.0`, changelog, 5-minute demo script + recording.

**Exit criteria**
* A stranger can reproduce the demos from the README with no help.
* Every claim in the README has a number next to it.
* `git log` reads as a story; each phase is bisectable.

**Demo:** the 5-step script (tenant on → quota change → backend kill → CP kill → canary).

---

## 3. Schedule (part-time, evenings)

| Week | Phase | Deliverable at the end of the week |
| :--- | :--- | :--- |
| 1 | P0 | CI + smoke test green |
| 2 | P1a | compile pipeline + tests |
| 3 | P1b | ADS + NACK + `pyloros` CLI |
| 4 | P2 | HCM/RDS + TLS/SDS + traces |
| 5 | P3a | `ext_authz` Java |
| 6 | P3b + P4 | RLS + resilience + first chaos drill |
| 7 | P5a | load report + drills table |
| 8 | P5b | ADRs + runbook + postmortem + v1.0 |

---

## 4. MVP if time collapses (cut here, in this order)

1. Drop the CLI (`curl` the API instead) — keep compile/validate.
2. Drop TLS/SDS — keep JWT.
3. Drop `ext_proc` — `ext_authz` covers the story.
4. Drop multi-region/locality — keep single Compose network.
5. Drop RLS — keep `local_ratelimit` (and say why: per-pod, not global).

**Never cut:** NACK metrics, rollback, one chaos drill, one ADR set.

---

## 5. Release checklist (v1.0)

- [ ] README: diagram, 3 numbers, 5-minute demo, honest limitations
- [ ] `make up|down|smoke|load|chaos` all work from a clean clone
- [ ] CI: build + vet + envoy validate + smoke
- [ ] Images pinned by digest; `.env.example` for every variable
- [ ] ADR index (6–10 decisions, each with the alternative)
- [ ] SLO doc + error-budget policy
- [ ] Runbook (5 alarms, first 3 actions each)
- [ ] Drills table (expected vs observed)
- [ ] One postmortem
- [ ] Cost/capacity one-pager
- [ ] Changelog + tag `v1.0` + demo recording
- [ ] LICENSE and CONTRIBUTING (bus-factor hygiene)

---

## 6. Risks and mitigations

| Risk | Mitigation |
| :--- | :--- |
| Scope creep into "build Istio" | non-goals are written in the README; MVP cut list above |
| Java/Go split doubles the maintenance | P0 fixes the protobuf contracts; no shared config files |
| No time for evidence | evidence is a *phase exit criterion*, not a final task |
| Demos work only on your laptop | `make smoke` in CI from week 1 |
| Overclaiming in docs | every claim needs a number or a command |

---

## 7. What to carry from the course repo

| From `tetrate-course-envoy` | Into Pyloros |
| :--- | :--- |
| `grpc-control-plane/main.go` | seed for `internal/xds` (ADS, snapshot, `constantHash` → real hashing) |
| `envoy-grpc.json` | seed for `envoy/bootstrap.json` |
| `LEARNINGS.md` §9 (object map) | `docs/adr/0002-object-model.md` |
| `LEARNINGS.md` §10 (interception map) | `docs/adr/0003-where-policy-lives.md` |
| NACK/version lessons | deleting them would repeat the same bugs |

---

**Next step:** P0 in a new repo, one commit per block
(`chore: skeleton`, `docs: boundaries adr`, `ci: validate configs`, `feat: compose baseline`).
