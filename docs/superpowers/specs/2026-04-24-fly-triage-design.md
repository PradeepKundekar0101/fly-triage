# fly-triage: AI-Powered Telemetry Diagnostic CLI — Design Spec

## Overview

A hybrid Go/Node.js CLI tool that helps Tier 1 support teams diagnose Fly Machine hardware and state-machine failures. Go handles high-speed log filtering; a TypeScript pipeline powered by Claude Sonnet analyzes anomalies and produces plain-English diagnoses with safe remediation steps.

## Architecture

```
fly-triage/
├── cli/                    # Go CLI binary
│   └── main.go
├── agent/                  # Node/TS agent + mock generator
│   ├── src/
│   │   ├── generate.ts     # Mock telemetry generator
│   │   └── agent.ts        # Diagnostic pipeline
│   ├── package.json
│   └── tsconfig.json
├── tmp/                    # gitignored, temp files
└── docs/
```

**Key decisions:**
- No LangGraph — the pipeline is strictly linear (ingest → analyze → resolve), so we use the Anthropic SDK directly.
- IPC is file-based: Go writes a temp JSON file, Node reads it and outputs to stdout.
- Monorepo with separate Go/Node directories. Each side has its own toolchain.

## Component 1: Mock Telemetry Generator

**File:** `agent/src/generate.ts`

**Purpose:** Generate a realistic `machine_events.jsonl` file with injected fault patterns buried in noise.

**JSONL schema:**
```json
{
  "timestamp": "2026-04-24T10:32:01.442Z",
  "machine_id": "m_8x4k2n",
  "host_id": "host_fdaa_ord1_77",
  "level": "info | warning | error",
  "source": "flyd | nvme | corrosion | health | network",
  "message": "string"
}
```

**Generation rules:**
- Produces 5,000–10,000 JSONL lines
- ~95% are `info`-level noise (routine events: machine started, health check passed, volume mounted, etc.)
- ~5% are `warning`/`error` with two specific fault patterns injected

**Fault Pattern 1 — NVMe failure chain:**
Correlated by a single `machine_id`, temporally clustered within a 30-second window:
1. `{ level: "warning", source: "nvme", message: "NVMe read timeout on /dev/nvme0n1 — latency 12400ms" }`
2. `{ level: "error", source: "nvme", message: "NVMe device /dev/nvme0n1 not responding — aborting pending I/O" }`
3. `{ level: "error", source: "flyd", message: "Health check failed for machine <id> — volume read error" }`
4. `{ level: "error", source: "flyd", message: "Machine <id> entered state 'failed' — cannot transition from 'starting'" }`

**Fault Pattern 2 — Network partition (Corrosion gossip):**
Correlated by a single `machine_id`, temporally clustered:
1. `{ level: "warning", source: "corrosion", message: "Gossip round timeout — no response from peer <peer_id> in 5000ms" }`
2. `{ level: "warning", source: "corrosion", message: "Gossip round timeout — no response from 3/5 peers" }`
3. `{ level: "error", source: "network", message: "Cluster partition detected — node isolated from region ord1" }`
4. `{ level: "error", source: "flyd", message: "Machine <id> unreachable — marking as 'lost'" }`

**Output:** Writes to `./machine_events.jsonl` in the current working directory.

**Invocation:** `npx tsx agent/src/generate.ts` or compiled `node agent/dist/generate.js`

## Component 2: Go Pre-processor CLI

**File:** `cli/main.go`

**Purpose:** Stream-read a JSONL file, strip info-level noise, write filtered output, invoke the Node agent, and pretty-print results.

**CLI interface:**
```
fly-triage --file <path-to-jsonl>
```

**Processing pipeline:**
1. Parse `--file` flag using Go `flag` stdlib
2. Open file, stream with `bufio.Scanner`
3. For each line: if the line contains `"level":"info"` (simple string contains check — avoids full JSON parse for speed), skip it. Otherwise, collect it.
4. Write collected lines as a JSON array to `tmp/filtered_<unix_timestamp>.json`
5. Execute subprocess: `node agent/dist/agent.js tmp/filtered_<timestamp>.json`
6. Capture stdout from the Node process
7. Parse the JSON response and pretty-print with ANSI colors

**Terminal output format:**
```
━━━ fly-triage ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  Processed: 7,234 lines
  Filtered:  6,891 info lines removed
  Anomalies: 343 warning/error lines

━━━ Diagnosis ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  Machine:  m_8x4k2n                    [RED]
  Severity: critical
  Cause:    NVMe hardware degradation on host_fdaa_ord1_77
            caused boot failure — machine stuck in 'starting'
            state due to unrecoverable volume read error.

  Action:   fly machine clone m_8x4k2n --region ord
            Then cordon original host: fly machine cordon host_fdaa_ord1_77

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

**Color coding:**
- `critical` severity → red
- `warning` severity → yellow
- `resolved`/`info` → green

**Error handling:**
- Missing `--file` flag → print usage and exit 1
- File not found → print error and exit 1
- Node process exits non-zero → print stderr and exit 1
- Node output is not valid JSON → print raw output with warning

## Component 3: TypeScript Diagnostic Pipeline

**File:** `agent/src/agent.ts`

**Purpose:** Analyze filtered error logs using Claude Sonnet and produce a structured diagnosis.

**Pipeline steps:**

### Step 1: Ingest
- Read temp JSON file path from `process.argv[2]`
- Parse the JSON array of log entries
- Group entries by `machine_id`
- For each machine, sort entries by timestamp

### Step 2: Analyze
- For each affected `machine_id`, send the error sequence to Claude Sonnet
- System prompt contains Fly.io domain knowledge:
  - flyd state machine transitions (created → starting → started → stopping → stopped → failed/destroyed)
  - Common NVMe failure signatures and what they mean for volume-backed machines
  - Corrosion gossip protocol basics and what partition detection implies
  - The difference between a machine that can be restarted vs. one that needs cloning
- User prompt contains the chronologically-ordered error sequence for that machine
- Model: `claude-sonnet-4-20250514`
- Ask for: root cause identification, severity assessment, affected components

### Step 3: Resolve
- Second LLM call with the analysis from Step 2
- Ask for: a specific, safe remediation command (no destructive operations)
- Constrain output to Fly.io CLI commands only (`fly machine clone`, `fly machine restart`, `fly machine cordon`, etc.)

**Output:** JSON to stdout:
```json
[
  {
    "machine_id": "m_8x4k2n",
    "host_id": "host_fdaa_ord1_77",
    "root_cause": "NVMe hardware degradation causing volume read failures, blocking machine boot sequence",
    "severity": "critical",
    "affected_components": ["nvme", "flyd"],
    "recommended_action": "fly machine clone m_8x4k2n --region ord"
  }
]
```

Returns an array since multiple machines may be affected.

## Domain Knowledge (System Prompt Content)

The agent's system prompt includes a condensed reference:

```
You are a Fly.io infrastructure diagnostic agent. You analyze machine telemetry to identify root causes and recommend safe remediations.

Key domain knowledge:

FLYD STATE MACHINE:
- Normal lifecycle: created → starting → started → stopping → stopped → destroyed
- Failure states: 'failed' (recoverable) and 'lost' (needs investigation)
- A machine stuck in 'starting' with volume errors indicates storage-layer failure
- A machine marked 'lost' with gossip timeouts indicates network partition

NVME FAILURES:
- Read timeouts >5000ms indicate degrading hardware
- "aborting pending I/O" means the device is unrecoverable on that host
- Resolution: clone the machine to a new host, do NOT restart on same host

CORROSION GOSSIP:
- Gossip timeout from 1 peer = transient, may self-heal
- Gossip timeout from majority of peers = network partition
- Partition → machine marked 'lost' → needs manual intervention
- Resolution: verify network, then restart machine or clone to different region

SAFETY RULES:
- Never recommend 'fly machine destroy' as a first action
- Prefer 'fly machine clone' over restart when hardware is suspected
- Always specify --region when cloning away from a bad host
```

## Dependencies

**Go (cli/):**
- Go 1.21+ (stdlib only, no external deps)

**Node (agent/):**
- `@anthropic-ai/sdk` — Claude API client
- `typescript` — build toolchain
- `tsx` — dev runner (optional, for generate script)
- No other runtime dependencies

## Environment

- Requires `ANTHROPIC_API_KEY` environment variable for the Node agent
- Go CLI looks for the Node agent at a relative path `agent/dist/agent.js` from the binary location

## Out of Scope

- Live Fly.io API integration
- gRPC or HTTP-based IPC
- Unit/integration test suites
- Config files or persistent state
- Multiple output formats (JSON-only from agent, pretty-print from Go)
