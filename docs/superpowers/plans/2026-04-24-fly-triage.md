# fly-triage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a hybrid Go/Node.js CLI that filters telemetry logs and uses Claude Sonnet to diagnose Fly Machine failures.

**Architecture:** Go CLI streams JSONL, filters out info-level noise, writes filtered errors to a temp file, then invokes a TypeScript pipeline that sends the errors to Claude for root-cause analysis and remediation. IPC is file-based (temp JSON file) + stdout.

**Tech Stack:** Go 1.21+ (stdlib only), TypeScript, `@anthropic-ai/sdk`, Node.js

---

## File Structure

```
fly-triage/
├── cli/
│   └── main.go                 # Go CLI: flag parsing, log filtering, subprocess exec, pretty-print
├── agent/
│   ├── src/
│   │   ├── generate.ts         # Mock telemetry generator (5k-10k JSONL lines)
│   │   └── agent.ts            # Diagnostic pipeline: ingest → analyze → resolve
│   ├── package.json
│   └── tsconfig.json
├── tmp/                        # gitignored, filtered logs land here
├── .gitignore
└── docs/
```

---

### Task 1: Project Scaffolding

**Files:**
- Create: `.gitignore`
- Create: `agent/package.json`
- Create: `agent/tsconfig.json`

- [ ] **Step 1: Create .gitignore**

```gitignore
# Node
node_modules/
agent/dist/

# Temp files
tmp/

# OS
.DS_Store
```

- [ ] **Step 2: Create agent/package.json**

```json
{
  "name": "fly-triage-agent",
  "version": "1.0.0",
  "private": true,
  "type": "module",
  "scripts": {
    "build": "tsc",
    "generate": "tsx src/generate.ts",
    "agent": "node dist/agent.js"
  },
  "dependencies": {
    "@anthropic-ai/sdk": "^0.39.0"
  },
  "devDependencies": {
    "typescript": "^5.7.0",
    "tsx": "^4.19.0"
  }
}
```

- [ ] **Step 3: Create agent/tsconfig.json**

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ES2022",
    "moduleResolution": "node16",
    "outDir": "./dist",
    "rootDir": "./src",
    "strict": true,
    "esModuleInterop": true,
    "declaration": false,
    "sourceMap": false
  },
  "include": ["src/**/*.ts"]
}
```

- [ ] **Step 4: Create tmp/ directory and install dependencies**

```bash
mkdir -p tmp
cd agent && npm install
```

- [ ] **Step 5: Initialize Go module**

```bash
cd cli && go mod init fly-triage/cli
```

- [ ] **Step 6: Verify setup**

```bash
cd agent && npx tsc --version
# Expected: Version 5.x.x
```

- [ ] **Step 7: Commit**

```bash
git add .gitignore agent/package.json agent/tsconfig.json agent/package-lock.json cli/go.mod
git commit -m "chore: scaffold project structure with Go module and Node package"
```

---

### Task 2: Mock Telemetry Generator

**Files:**
- Create: `agent/src/generate.ts`

- [ ] **Step 1: Create the generator script**

```typescript
import { writeFileSync } from "fs";

interface LogEntry {
  timestamp: string;
  machine_id: string;
  host_id: string;
  level: "info" | "warning" | "error";
  source: "flyd" | "nvme" | "corrosion" | "health" | "network";
  message: string;
}

const MACHINE_IDS = [
  "m_8x4k2n", "m_3r7p1q", "m_9w2j5v", "m_1k6t8m", "m_4f0d3x",
  "m_7h2y9c", "m_5n8b1g", "m_2s6w4r", "m_6a3e7l", "m_0p5u2z",
];
const HOST_IDS = [
  "host_fdaa_ord1_77", "host_fdaa_ord1_42", "host_fdaa_iad1_13",
  "host_fdaa_sjc1_05", "host_fdaa_lhr1_22",
];
const REGIONS = ["ord1", "iad1", "sjc1", "lhr1"];

const INFO_MESSAGES: Array<{ source: LogEntry["source"]; message: string }> = [
  { source: "flyd", message: "Machine {id} transitioned to 'started'" },
  { source: "flyd", message: "Machine {id} health check passed" },
  { source: "flyd", message: "Machine {id} volume mounted successfully" },
  { source: "health", message: "Health check OK for machine {id} — response 200 in 12ms" },
  { source: "health", message: "Health check OK for machine {id} — response 200 in 8ms" },
  { source: "flyd", message: "Machine {id} allocated 256MB memory on {host}" },
  { source: "network", message: "WireGuard tunnel established for machine {id}" },
  { source: "flyd", message: "Machine {id} image pull completed in 1.2s" },
  { source: "flyd", message: "Machine {id} proxy routing updated" },
  { source: "corrosion", message: "Gossip round completed — 5/5 peers healthy" },
  { source: "health", message: "Volume /dev/nvme0n1 SMART status: OK" },
  { source: "network", message: "DNS resolution OK for machine {id}" },
  { source: "flyd", message: "Machine {id} graceful shutdown completed" },
  { source: "flyd", message: "Machine {id} transitioned to 'stopped'" },
  { source: "flyd", message: "Machine {id} scheduled restart in 30s" },
];

function pick<T>(arr: T[]): T {
  return arr[Math.floor(Math.random() * arr.length)];
}

function randomTimestamp(base: Date, offsetMs: number): string {
  return new Date(base.getTime() + offsetMs).toISOString();
}

function generateInfoLine(base: Date, offsetMs: number): LogEntry {
  const machineId = pick(MACHINE_IDS);
  const hostId = pick(HOST_IDS);
  const tmpl = pick(INFO_MESSAGES);
  return {
    timestamp: randomTimestamp(base, offsetMs),
    machine_id: machineId,
    host_id: hostId,
    level: "info",
    source: tmpl.source,
    message: tmpl.message.replace("{id}", machineId).replace("{host}", hostId),
  };
}

function generateNvmeFault(base: Date, offsetMs: number): LogEntry[] {
  const machineId = "m_8x4k2n";
  const hostId = "host_fdaa_ord1_77";
  return [
    {
      timestamp: randomTimestamp(base, offsetMs),
      machine_id: machineId, host_id: hostId,
      level: "warning", source: "nvme",
      message: "NVMe read timeout on /dev/nvme0n1 — latency 12400ms",
    },
    {
      timestamp: randomTimestamp(base, offsetMs + 5000),
      machine_id: machineId, host_id: hostId,
      level: "error", source: "nvme",
      message: "NVMe device /dev/nvme0n1 not responding — aborting pending I/O",
    },
    {
      timestamp: randomTimestamp(base, offsetMs + 12000),
      machine_id: machineId, host_id: hostId,
      level: "error", source: "flyd",
      message: `Health check failed for machine ${machineId} — volume read error`,
    },
    {
      timestamp: randomTimestamp(base, offsetMs + 20000),
      machine_id: machineId, host_id: hostId,
      level: "error", source: "flyd",
      message: `Machine ${machineId} entered state 'failed' — cannot transition from 'starting'`,
    },
  ];
}

function generateNetworkFault(base: Date, offsetMs: number): LogEntry[] {
  const machineId = "m_3r7p1q";
  const hostId = "host_fdaa_iad1_13";
  const peerId = "peer_fdaa_iad1_08";
  return [
    {
      timestamp: randomTimestamp(base, offsetMs),
      machine_id: machineId, host_id: hostId,
      level: "warning", source: "corrosion",
      message: `Gossip round timeout — no response from peer ${peerId} in 5000ms`,
    },
    {
      timestamp: randomTimestamp(base, offsetMs + 8000),
      machine_id: machineId, host_id: hostId,
      level: "warning", source: "corrosion",
      message: "Gossip round timeout — no response from 3/5 peers",
    },
    {
      timestamp: randomTimestamp(base, offsetMs + 15000),
      machine_id: machineId, host_id: hostId,
      level: "error", source: "network",
      message: "Cluster partition detected — node isolated from region iad1",
    },
    {
      timestamp: randomTimestamp(base, offsetMs + 22000),
      machine_id: machineId, host_id: hostId,
      level: "error", source: "flyd",
      message: `Machine ${machineId} unreachable — marking as 'lost'`,
    },
  ];
}

function generate(): void {
  const totalLines = 7000 + Math.floor(Math.random() * 3000); // 7000-10000
  const base = new Date("2026-04-24T10:00:00.000Z");
  const spanMs = 3600_000; // 1 hour of logs

  const lines: LogEntry[] = [];

  // Generate info noise
  for (let i = 0; i < totalLines; i++) {
    const offset = Math.floor(Math.random() * spanMs);
    lines.push(generateInfoLine(base, offset));
  }

  // Inject NVMe fault chain ~20 minutes in
  const nvmeFault = generateNvmeFault(base, 1_200_000);
  lines.push(...nvmeFault);

  // Inject network partition ~40 minutes in
  const netFault = generateNetworkFault(base, 2_400_000);
  lines.push(...netFault);

  // Sort by timestamp
  lines.sort((a, b) => a.timestamp.localeCompare(b.timestamp));

  // Write as JSONL
  const output = lines.map((l) => JSON.stringify(l)).join("\n") + "\n";
  writeFileSync("machine_events.jsonl", output);
  console.log(`Generated ${lines.length} log entries to machine_events.jsonl`);
  console.log(`  - ${totalLines} info lines`);
  console.log(`  - ${nvmeFault.length} NVMe fault entries (machine m_8x4k2n)`);
  console.log(`  - ${netFault.length} network fault entries (machine m_3r7p1q)`);
}

generate();
```

- [ ] **Step 2: Build and run the generator**

```bash
cd agent && npx tsx src/generate.ts
```

Expected output:
```
Generated ~7008 log entries to machine_events.jsonl
  - ~7000 info lines
  - 4 NVMe fault entries (machine m_8x4k2n)
  - 4 network fault entries (machine m_3r7p1q)
```

- [ ] **Step 3: Verify the output file has injected faults**

```bash
grep -c "error" machine_events.jsonl
# Expected: ~6-8 lines containing "error"
grep "nvme\|corrosion\|partition" machine_events.jsonl | head -5
# Should show the injected fault entries
```

- [ ] **Step 4: Commit**

```bash
git add agent/src/generate.ts
git commit -m "feat: add mock telemetry generator with NVMe and network fault injection"
```

---

### Task 3: TypeScript Diagnostic Agent

**Files:**
- Create: `agent/src/agent.ts`

- [ ] **Step 1: Create the agent pipeline**

```typescript
import Anthropic from "@anthropic-ai/sdk";
import { readFileSync } from "fs";

interface LogEntry {
  timestamp: string;
  machine_id: string;
  host_id: string;
  level: "info" | "warning" | "error";
  source: string;
  message: string;
}

interface Diagnosis {
  machine_id: string;
  host_id: string;
  root_cause: string;
  severity: "critical" | "warning" | "info";
  affected_components: string[];
  recommended_action: string;
}

const SYSTEM_PROMPT = `You are a Fly.io infrastructure diagnostic agent. You analyze machine telemetry to identify root causes and recommend safe remediations.

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
- Always specify --region when cloning away from a bad host`;

// Step 1: Ingest
function ingest(filePath: string): Map<string, LogEntry[]> {
  const raw = readFileSync(filePath, "utf-8");
  const entries: LogEntry[] = JSON.parse(raw);

  const grouped = new Map<string, LogEntry[]>();
  for (const entry of entries) {
    const existing = grouped.get(entry.machine_id) || [];
    existing.push(entry);
    grouped.set(entry.machine_id, existing);
  }

  // Sort each group by timestamp
  for (const [, entries] of grouped) {
    entries.sort((a, b) => a.timestamp.localeCompare(b.timestamp));
  }

  return grouped;
}

// Step 2: Analyze
async function analyze(
  client: Anthropic,
  machineId: string,
  entries: LogEntry[]
): Promise<{ analysis: string; hostId: string }> {
  const hostId = entries[0].host_id;
  const logText = entries
    .map((e) => `[${e.timestamp}] [${e.level}] [${e.source}] ${e.message}`)
    .join("\n");

  const response = await client.messages.create({
    model: "claude-sonnet-4-20250514",
    max_tokens: 1024,
    system: SYSTEM_PROMPT,
    messages: [
      {
        role: "user",
        content: `Analyze this error sequence for machine ${machineId} on host ${hostId}. Identify the root cause, severity (critical/warning/info), and affected components.

Error sequence:
${logText}

Respond in this exact JSON format, no other text:
{"root_cause": "...", "severity": "critical|warning|info", "affected_components": ["..."]}`,
      },
    ],
  });

  const text =
    response.content[0].type === "text" ? response.content[0].text : "";
  return { analysis: text, hostId };
}

// Step 3: Resolve
async function resolve(
  client: Anthropic,
  machineId: string,
  hostId: string,
  analysis: string
): Promise<string> {
  const response = await client.messages.create({
    model: "claude-sonnet-4-20250514",
    max_tokens: 512,
    system: SYSTEM_PROMPT,
    messages: [
      {
        role: "user",
        content: `Based on this analysis for machine ${machineId} on host ${hostId}, provide a specific, safe remediation using Fly.io CLI commands only.

Analysis: ${analysis}

Respond with ONLY the recommended CLI command(s), one per line. No explanation.`,
      },
    ],
  });

  return response.content[0].type === "text" ? response.content[0].text : "";
}

async function main(): Promise<void> {
  const filePath = process.argv[2];
  if (!filePath) {
    console.error("Usage: node agent.js <filtered-logs.json>");
    process.exit(1);
  }

  const client = new Anthropic();
  const grouped = ingest(filePath);
  const diagnoses: Diagnosis[] = [];

  for (const [machineId, entries] of grouped) {
    const { analysis, hostId } = await analyze(client, machineId, entries);

    let parsed: { root_cause: string; severity: string; affected_components: string[] };
    try {
      parsed = JSON.parse(analysis);
    } catch {
      // If LLM didn't return clean JSON, extract what we can
      parsed = {
        root_cause: analysis,
        severity: "warning",
        affected_components: ["unknown"],
      };
    }

    const action = await resolve(client, machineId, hostId, analysis);

    diagnoses.push({
      machine_id: machineId,
      host_id: hostId,
      root_cause: parsed.root_cause,
      severity: parsed.severity as Diagnosis["severity"],
      affected_components: parsed.affected_components,
      recommended_action: action.trim(),
    });
  }

  // Output JSON to stdout
  console.log(JSON.stringify(diagnoses, null, 2));
}

main().catch((err) => {
  console.error("Agent error:", err.message);
  process.exit(1);
});
```

- [ ] **Step 2: Build the TypeScript**

```bash
cd agent && npm run build
```

Expected: `dist/agent.js` and `dist/generate.js` created without errors.

- [ ] **Step 3: Smoke test with a small manual input**

Create a small test file to verify the agent loads and runs:

```bash
echo '[{"timestamp":"2026-04-24T10:20:00Z","machine_id":"m_test","host_id":"host_test","level":"error","source":"nvme","message":"NVMe read timeout on /dev/nvme0n1 — latency 12400ms"}]' > tmp/test_input.json
cd agent && ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY node dist/agent.js ../tmp/test_input.json
```

Expected: JSON array printed to stdout with a diagnosis for `m_test`.

- [ ] **Step 4: Commit**

```bash
git add agent/src/agent.ts
git commit -m "feat: add TypeScript diagnostic agent with Claude Sonnet pipeline"
```

---

### Task 4: Go CLI Pre-processor

**Files:**
- Create: `cli/main.go`

- [ ] **Step 1: Create the Go CLI**

```go
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Diagnosis struct {
	MachineID          string   `json:"machine_id"`
	HostID             string   `json:"host_id"`
	RootCause          string   `json:"root_cause"`
	Severity           string   `json:"severity"`
	AffectedComponents []string `json:"affected_components"`
	RecommendedAction  string   `json:"recommended_action"`
}

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorGreen  = "\033[32m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
)

func severityColor(severity string) string {
	switch severity {
	case "critical":
		return colorRed
	case "warning":
		return colorYellow
	default:
		return colorGreen
	}
}

func main() {
	filePath := flag.String("file", "", "Path to JSONL telemetry file")
	flag.Parse()

	if *filePath == "" {
		fmt.Fprintf(os.Stderr, "Usage: fly-triage --file <path-to-jsonl>\n")
		os.Exit(1)
	}

	// Open and stream the file
	f, err := os.Open(*filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot open file %s: %v\n", *filePath, err)
		os.Exit(1)
	}
	defer f.Close()

	startTime := time.Now()
	scanner := bufio.NewScanner(f)
	// Increase buffer size for long lines
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var filtered []json.RawMessage
	totalLines := 0
	infoLines := 0

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		totalLines++

		// Fast string-contains check to skip info lines
		if strings.Contains(line, `"level":"info"`) || strings.Contains(line, `"level": "info"`) {
			infoLines++
			continue
		}

		filtered = append(filtered, json.RawMessage(line))
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	// Write filtered output to temp file
	tmpDir := "tmp"
	os.MkdirAll(tmpDir, 0o755)
	tmpFile := filepath.Join(tmpDir, fmt.Sprintf("filtered_%d.json", time.Now().Unix()))

	out, err := json.Marshal(filtered)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling filtered data: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(tmpFile, out, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing temp file: %v\n", err)
		os.Exit(1)
	}

	filterDuration := time.Since(startTime)
	anomalyCount := totalLines - infoLines

	// Print filtering stats
	fmt.Printf("\n%s━━━ fly-triage ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n\n", colorBold, colorReset)
	fmt.Printf("  Processed: %s%d%s lines in %s\n", colorBold, totalLines, colorReset, filterDuration.Round(time.Millisecond))
	fmt.Printf("  Filtered:  %s%d%s info lines removed\n", colorDim, infoLines, colorReset)
	fmt.Printf("  Anomalies: %s%d%s warning/error lines\n", colorBold, anomalyCount, colorReset)
	fmt.Printf("\n  Analyzing with Claude Sonnet...\n")

	// Execute Node agent
	agentPath := filepath.Join("agent", "dist", "agent.js")
	cmd := exec.Command("node", agentPath, tmpFile)
	cmd.Stderr = os.Stderr

	output, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError running agent: %v\n", err)
		os.Exit(1)
	}

	// Parse diagnoses
	var diagnoses []Diagnosis
	if err := json.Unmarshal(output, &diagnoses); err != nil {
		fmt.Printf("\n%s━━━ Raw Agent Output ━━━━━━━━━━━━━━━━━━━━━━━%s\n", colorYellow, colorReset)
		fmt.Printf("  Warning: could not parse agent JSON\n\n")
		fmt.Println(string(output))
		os.Exit(0)
	}

	// Pretty print each diagnosis
	for _, d := range diagnoses {
		color := severityColor(d.Severity)
		fmt.Printf("\n%s━━━ Diagnosis ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n\n", colorBold, colorReset)
		fmt.Printf("  Machine:  %s%s%s\n", color, d.MachineID, colorReset)
		fmt.Printf("  Host:     %s\n", d.HostID)
		fmt.Printf("  Severity: %s%s%s\n", color, d.Severity, colorReset)
		fmt.Printf("  Components: %s\n", strings.Join(d.AffectedComponents, ", "))
		fmt.Printf("\n  %sCause:%s\n", colorBold, colorReset)
		// Word-wrap the root cause at ~60 chars
		for _, line := range wordWrap(d.RootCause, 60) {
			fmt.Printf("    %s\n", line)
		}
		fmt.Printf("\n  %sAction:%s\n", colorBold, colorReset)
		for _, line := range strings.Split(d.RecommendedAction, "\n") {
			fmt.Printf("    %s%s%s\n", colorGreen, line, colorReset)
		}
	}

	fmt.Printf("\n%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n\n", colorDim, colorReset)
}

func wordWrap(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}

	var lines []string
	current := words[0]

	for _, word := range words[1:] {
		if len(current)+1+len(word) > width {
			lines = append(lines, current)
			current = word
		} else {
			current += " " + word
		}
	}
	lines = append(lines, current)
	return lines
}
```

- [ ] **Step 2: Build the Go binary**

```bash
cd cli && go build -o ../fly-triage .
```

Expected: `fly-triage` binary created in project root with no errors.

- [ ] **Step 3: Test flag parsing**

```bash
./fly-triage
# Expected: "Usage: fly-triage --file <path-to-jsonl>" and exit code 1

./fly-triage --file nonexistent.jsonl
# Expected: "Error: cannot open file nonexistent.jsonl: ..." and exit code 1
```

- [ ] **Step 4: Commit**

```bash
git add cli/main.go
git commit -m "feat: add Go CLI for log filtering and agent invocation"
```

---

### Task 5: End-to-End Integration

**Files:** No new files. Uses everything built in Tasks 1-4.

- [ ] **Step 1: Generate mock telemetry data**

```bash
cd agent && npx tsx src/generate.ts
```

Expected: `machine_events.jsonl` created with ~7000+ lines.

- [ ] **Step 2: Build both components**

```bash
cd agent && npm run build && cd ../cli && go build -o ../fly-triage .
```

Expected: Both `agent/dist/agent.js` and `fly-triage` binary exist.

- [ ] **Step 3: Run the full pipeline**

Requires `ANTHROPIC_API_KEY` to be set.

```bash
./fly-triage --file machine_events.jsonl
```

Expected output:
1. Stats showing ~7000 lines processed, ~6900+ filtered, ~8 anomalies
2. Two diagnosis blocks: one for `m_8x4k2n` (NVMe fault, critical) and one for `m_3r7p1q` (network partition)
3. Each diagnosis includes a `fly machine clone` or similar remediation command
4. Color-coded terminal output

- [ ] **Step 4: Verify the temp file was created**

```bash
ls tmp/filtered_*.json
# Expected: one file exists
cat tmp/filtered_*.json | python3 -m json.tool | head -20
# Expected: JSON array of warning/error log entries
```

- [ ] **Step 5: Final commit**

```bash
git add -A
git commit -m "chore: build artifacts and generated test data for e2e verification"
```
