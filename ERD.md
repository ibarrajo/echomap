# EchoMap v2 — Latency-Based Geolocation

## The Core Idea

**Two layers of proof:**

1. **Hard bound (physics):** Light in fiber travels ~200 km/ms. RTT gives an absolute maximum distance from any probe. You can't fake being faster than light.
2. **Soft bound (datasets):** Known network latency datasets tell us what real RTTs look like between real network paths. A user claiming to be in Berlin should have RTTs that match what Berlin-to-probe latencies actually look like — not just within the speed-of-light circle, but within the statistical range of real-world measurements.

The hard bound catches blatant lies. The soft bound catches subtle ones.

---

## How It Works (30-Second Version)

```
1. Client calls gRPC FetchChallenge → gets a token + list of probe targets
2. Client pings each probe, collects RTTs
3. Client submits RTTs via gRPC SubmitMeasurement
4. Server computes:
   a. Hard bound: RTT → max distance circle per probe (speed of light)
   b. Soft bound: compare RTTs against known latency datasets for candidate regions
   c. Intersect circles + statistical match → region + confidence
5. Return: "User is within this region" + what's excluded + confidence
```

---

## Two-Layer Calculation

### Layer 1: Speed of Light (Hard Bound)

```
max_distance_km = (rtt_ms / 2) × 200

Example:
  RTT to Frankfurt probe: 12ms → max 1,200 km from Frankfurt
  RTT to London probe:     8ms → max   800 km from London
  RTT to Paris probe:      6ms → max   600 km from Paris

  Circle intersection → user is somewhere in Benelux / Northern France
```

This is the floor. No one can be farther than this. Spoofing can only ADD latency (bigger circles, less precision, never a wrong answer).

### Layer 2: Known Latency Datasets (Soft Bound)

Real network paths have **known, measurable latencies** that are published in free datasets. These are always slower than speed-of-light because of routing hops, congestion, and peering.

```
Dataset says: Frankfurt → Amsterdam typical RTT = 7-10ms
User reports: Frankfurt RTT = 8ms ✓ consistent with Amsterdam
User reports: Frankfurt RTT = 45ms ✗ inconsistent — they're farther or on a VPN

Dataset says: Frankfurt → Tokyo typical RTT = 230-260ms
User reports: Frankfurt RTT = 12ms ✗ impossible if user is in Tokyo
```

The server loads these datasets at startup and uses them to **tighten the circles** from Layer 1. Instead of "within 1,200 km of Frankfurt," we can say "latency profile matches Amsterdam/Brussels/Cologne region."

### Free Datasets

| Dataset | What It Provides | Update Frequency | Format |
|---------|-----------------|-------------------|--------|
| [RIPE Atlas](https://atlas.ripe.net/measurements/) | Millions of RTT measurements between 12,000+ probes worldwide | Continuous | JSON API |
| [WonderNetwork](https://wondernetwork.com/pings) | City-to-city ping times, 240+ cities | Monthly | CSV |
| [Globalping](https://www.globalping.io/) | On-demand latency measurements from global probes | Real-time API | JSON |
| [CAIDA Ark](https://www.caida.org/catalog/datasets/ark-ipv4/) | Internet topology + traceroute latencies | Ongoing | Custom |
| [iPlane](https://iplane.cs.washington.edu/) | Predicted path latencies between arbitrary endpoints | Periodic | Text |

**Recommended starting point:** WonderNetwork CSV (simple, city-to-city, easy to parse) + Globalping API (real-time cross-validation).

---

## Why Spoofing Fails

| Attack | Effect on Hard Bound | Effect on Soft Bound | Result |
|--------|---------------------|---------------------|--------|
| VPN/Proxy | Circles get bigger | Latency profile won't match any real city | Flagged or "unknown" |
| Artificial delay | Circles get bigger | Jitter pattern is wrong, ratios between probes are off | Flagged |
| Replay | Stale token | — | Rejected (token TTL) |
| Claim wrong city | Can't reduce RTT | RTTs won't match dataset for claimed city | Rejected |

**Key insight:** A user can add latency, but the *ratio* between probe RTTs is extremely hard to fake. If you're in Amsterdam, your Frankfurt RTT will always be less than your New York RTT. Faking Amsterdam-like ratios from Tokyo requires precise per-probe delays that change with every challenge.

---

## Architecture

### Stack

| Component | Tech | Why |
|-----------|------|-----|
| Server | Go + gRPC + Uber FX | Strong typing, DI, fast, production-grade |
| Client | Go | Trusted device client, WASM-compilable later for web |
| Probes | Globalping API + own edge servers (later) | Free, global, no infra to manage |
| Storage | SQLite (dev) / PostgreSQL (prod) | Client history, anomaly logs, dataset cache |
| Dataset loader | In-process (Uber FX module) | Loads WonderNetwork CSV + RIPE data at startup |

### Component Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│                          Go Server (Uber FX)                        │
│                                                                     │
│  ┌──────────────┐  ┌──────────────┐  ┌────────────────────────────┐ │
│  │  gRPC Handler │  │  Challenge   │  │     Geolocation Engine     │ │
│  │              │  │  Manager     │  │                            │ │
│  │ FetchChallenge│→│ Token gen    │  │ ┌────────────────────────┐ │ │
│  │ SubmitMeasure │→│ Token verify │  │ │ Layer 1: Speed of Light│ │ │
│  │              │  │ Rate limit   │  │ │ Circle intersection    │ │ │
│  └──────────────┘  └──────────────┘  │ └────────────────────────┘ │ │
│                                      │ ┌────────────────────────┐ │ │
│  ┌──────────────┐  ┌──────────────┐  │ │ Layer 2: Dataset Match │ │ │
│  │  Dataset      │  │  Persistence │  │ │ Statistical comparison │ │ │
│  │  Loader       │  │              │  │ └────────────────────────┘ │ │
│  │              │  │ Client hist  │  │ ┌────────────────────────┐ │ │
│  │ WonderNetwork│  │ Anomaly logs │  │ │ Jitter / Ratio Checks │ │ │
│  │ RIPE Atlas   │  │ Result cache │  │ └────────────────────────┘ │ │
│  │ Globalping   │  │              │  └────────────────────────────┘ │
│  └──────────────┘  └──────────────┘                                 │
└─────────────────────────────────────────────────────────────────────┘
         ▲                                        │
         │ gRPC                          Globalping API (cross-validation)
         │                                        │
    ┌────┴─────┐                          ┌───────▼───────┐
    │ Go Client │                          │  Globalping   │
    │          │                          │  Probes       │
    └──────────┘                          └───────────────┘
```

---

## gRPC API

### Protobuf

```protobuf
syntax = "proto3";

package echomap.v1;

option go_package = "github.com/yourusername/echomap/proto/v1";

service EchoMap {
  // Get a challenge with probe targets to ping
  rpc FetchChallenge(ChallengeRequest) returns (ChallengeResponse);

  // Submit measured latencies for geolocation calculation
  rpc SubmitMeasurement(MeasurementRequest) returns (MeasurementResponse);
}

// --- Request/Response ---

message ChallengeRequest {
  string client_id = 1;
}

message ChallengeResponse {
  string challenge_id = 1;
  repeated ProbeTarget targets = 2;
  int32 timeout_ms = 3;          // Max time to complete all pings
  int64 expires_at = 4;          // Unix timestamp — challenge dies after this
}

message ProbeTarget {
  string id = 1;                  // e.g. "fra-1"
  string host = 2;               // e.g. "fra.probes.echomap.dev"
  int32 port = 3;
  int32 ping_count = 4;          // How many pings to send (typically 3)
}

message MeasurementRequest {
  string challenge_id = 1;
  repeated ProbeMeasurement measurements = 2;
}

message ProbeMeasurement {
  string probe_id = 1;
  repeated int32 rtts_us = 2;    // Microsecond precision, multiple pings
}

message MeasurementResponse {
  Verdict verdict = 1;
  Region region = 2;
  repeated Exclusion exclusions = 3;
  repeated ProbeResult probe_results = 4;
  SpoofingIndicators spoofing = 5;
}

// --- Types ---

message Verdict {
  Status status = 1;
  double confidence = 2;         // 0.0 - 1.0
}

enum Status {
  STATUS_UNSPECIFIED = 0;
  STATUS_CONFIRMED = 1;          // High confidence region match
  STATUS_PLAUSIBLE = 2;          // Consistent but imprecise
  STATUS_SUSPICIOUS = 3;         // Latency anomalies detected
  STATUS_REJECTED = 4;           // Physically impossible or spoofed
}

message Region {
  double lat = 1;
  double lon = 2;
  double radius_km = 3;
  string label = 4;              // e.g. "Western Europe"
}

message Exclusion {
  string region = 1;             // e.g. "East Asia"
  double confidence = 2;
}

message ProbeResult {
  string probe_id = 1;
  double rtt_ms = 2;             // Median of pings
  double jitter_ms = 3;
  double max_distance_km = 4;    // Speed-of-light bound
  double dataset_expected_ms = 5; // What the dataset says RTT should be for best-fit region
}

message SpoofingIndicators {
  bool vpn_likely = 1;
  bool jitter_abnormal = 2;
  bool ratio_inconsistent = 3;   // Probe-to-probe ratios don't match any known city
  bool physically_impossible = 4; // RTT violates speed of light for claimed location
}
```

---

## Uber FX Module Layout

```go
// cmd/echomap/main.go
func main() {
    fx.New(
        fx.Provide(
            config.New,             // Load config from env/file
            dataset.NewLoader,      // Load WonderNetwork + RIPE data
            challenge.NewManager,   // Token generation + validation
            geo.NewEngine,          // Geolocation calculation engine
            storage.NewRepository,  // SQLite/Postgres
            grpcserver.New,         // gRPC server setup
        ),
        fx.Invoke(
            dataset.StartPeriodicRefresh,  // Refresh datasets on interval
            grpcserver.Register,           // Register gRPC handlers
        ),
    ).Run()
}
```

### Uber FX Modules

| Module | Responsibility |
|--------|---------------|
| `config` | Env vars, probe list, dataset paths, token TTL |
| `dataset` | Parse WonderNetwork CSV, query RIPE Atlas API, build lookup table: `(city_a, city_b) → expected_rtt_range` |
| `challenge` | Generate challenge tokens (HMAC-signed, 10s TTL), select probes, validate on return |
| `geo` | Layer 1 (circle intersection) + Layer 2 (dataset matching) + jitter analysis → verdict |
| `storage` | Persist results, client history, anomaly log |
| `grpcserver` | gRPC listener, TLS, interceptors (rate limit, logging) |

---

## Geolocation Engine Detail

### Input

```go
type Measurement struct {
    ProbeID   string      // Which probe
    ProbeLat  float64     // Probe's known location
    ProbeLon  float64
    RTTs      []int       // Microseconds, multiple pings
}
```

### Calculation Steps

```go
func (e *Engine) Locate(measurements []Measurement) *Result {
    // 1. Hard bound: speed-of-light circles
    circles := make([]Circle, len(measurements))
    for i, m := range measurements {
        medianRTT := median(m.RTTs)
        maxDist := float64(medianRTT) / 2.0 * 0.2  // μs → ms → km (200 km/ms)
        circles[i] = Circle{Lat: m.ProbeLat, Lon: m.ProbeLon, RadiusKM: maxDist}
    }
    hardRegion := IntersectCircles(circles)

    // 2. Soft bound: compare against dataset
    candidateCities := e.dataset.CitiesInRegion(hardRegion)
    bestMatch := e.dataset.BestLatencyMatch(candidateCities, measurements)

    // 3. Jitter analysis
    jitterOK := e.checkJitter(measurements)

    // 4. Ratio analysis — are probe-to-probe RTT ratios consistent with a real location?
    ratioOK := e.checkRatios(measurements, bestMatch)

    // 5. Build verdict
    return &Result{
        Region:     bestMatch.Region,
        Confidence: e.score(hardRegion, bestMatch, jitterOK, ratioOK),
        Exclusions: e.computeExclusions(circles),
        Spoofing:   e.spoofingIndicators(jitterOK, ratioOK, measurements),
    }
}
```

### Dataset Matching

```go
// e.dataset.BestLatencyMatch finds the city whose known latency profile
// best matches the observed RTTs.
//
// For each candidate city C:
//   For each probe P:
//     expected_rtt = dataset[C][P]   // e.g. WonderNetwork says Amsterdam→Frankfurt = 7-10ms
//     observed_rtt = measurement[P]
//     error += abs(observed_rtt - expected_rtt) / expected_rtt  // Normalized error
//   Score city C by total error
//
// Return city with lowest total error + the error magnitude as confidence input.
```

---

## Probe Strategy

### Probe Sources (Priority Order)

1. **Globalping API** — Free, 800+ probes, on-demand measurements. Use as primary.
2. **Own edge servers** — Deploy later for lower-latency, controlled probes.
3. **Public DNS/NTP** — Fallback: ping well-known servers with known locations (e.g., `dns.google` = us-east/us-west anycast).

### Probe Selection Per Challenge

```
1. Use client IP geolocation (MaxMind free DB) for rough region estimate
2. Select probes:
   - 3 probes near estimated region (for precision)
   - 2 probes on same continent but far (for sub-continental accuracy)
   - 1-2 probes on other continents (for continent-level exclusion)
3. Total: 6-7 probes, all pinged in parallel by client
```

---

## Project Layout

```
echomap/
├── cmd/
│   ├── echomap/           # Server binary
│   │   └── main.go
│   └── echomap-client/    # Go client binary
│       └── main.go
├── proto/
│   └── v1/
│       └── echomap.proto
├── internal/
│   ├── config/            # Uber FX config module
│   ├── challenge/         # Token gen, probe selection, validation
│   ├── dataset/           # Load + query WonderNetwork, RIPE, Globalping
│   ├── geo/               # Circle math, intersection, scoring
│   ├── grpcserver/        # gRPC setup + handlers
│   └── storage/           # SQLite/Postgres persistence
├── data/
│   └── wondernetwork/     # Cached CSV data
├── go.mod
├── go.sum
├── Makefile
└── ERD.md
```

---

## Build Phases

### Phase 1: Core Engine (Today)

**Goal:** gRPC server that accepts RTTs and returns a geolocation verdict.

- [ ] Protobuf + code generation
- [ ] `geo` module: Haversine distance, circle intersection, speed-of-light bound
- [ ] `challenge` module: HMAC token gen/verify, probe selection (hardcoded probe list)
- [ ] `grpcserver` module: `FetchChallenge` + `SubmitMeasurement` handlers
- [ ] `config` module: env-based config
- [ ] Uber FX wiring in `main.go`
- [ ] Go client: fetch challenge, ping probes (TCP connect timing), submit results
- [ ] Manual test: does it correctly locate you?

### Phase 2: Dataset Integration (Day 2)

- [ ] `dataset` module: parse WonderNetwork CSV into `map[(cityA, cityB)]RTTRange`
- [ ] Integrate dataset matching into `geo.Engine.Locate()`
- [ ] Add Globalping API client for on-demand cross-validation
- [ ] Add ratio analysis (probe-to-probe RTT ratios)
- [ ] Compare hard bound vs soft bound results

### Phase 3: Hardening (Day 3-4)

- [ ] Jitter analysis (flag zero-jitter or suspiciously consistent RTTs)
- [ ] SQLite persistence for results + anomaly log
- [ ] Rate limiting (gRPC interceptor)
- [ ] MaxMind GeoLite2 integration for initial IP-based region estimate
- [ ] Probe selection optimization based on region estimate
- [ ] Integration tests with known locations

---

## Success Criteria

- [ ] Can distinguish US East vs US West with >90% accuracy
- [ ] Can exclude wrong continents with >99% accuracy
- [ ] Total measurement + calculation completes in <1s
- [ ] Zero false positives — never confirms a wrong location
- [ ] VPN users get `STATUS_SUSPICIOUS`, not `STATUS_CONFIRMED`
- [ ] Dataset matching produces tighter regions than speed-of-light alone

---

## Limitations (Be Honest)

1. **Precision:** ~50-200 km radius best case. City-level, not street-level.
2. **VPN/proxy users:** Get "suspicious" or "plausible" — never a false confirmation.
3. **Mobile networks:** Higher base RTT and jitter. Need calibration.
4. **Dataset staleness:** Network topology changes. Datasets need periodic refresh.
5. **Anycast:** Some targets (like `8.8.8.8`) route to nearest node, not a fixed location. Probe list must account for this.

---

## References

- [RIPE Atlas](https://atlas.ripe.net/) — Free global measurement platform, 12,000+ probes
- [WonderNetwork Pings](https://wondernetwork.com/pings) — Monthly city-to-city ping data (CSV)
- [Globalping](https://www.globalping.io/) — Free on-demand latency API
- [CAIDA](https://www.caida.org/) — Internet topology datasets
- [Haversine Formula](https://en.wikipedia.org/wiki/Haversine_formula) — Great-circle distance
- [Speed of Light in Fiber](https://en.wikipedia.org/wiki/Optical_fiber#Index_of_refraction) — ~200,000 km/s
- [MaxMind GeoLite2](https://dev.maxmind.com/geoip/geolite2-free-geolocation-data) — Free IP geolocation DB
- [gRPC Go](https://pkg.go.dev/google.golang.org/grpc)
- [Uber FX](https://pkg.go.dev/go.uber.org/fx)
