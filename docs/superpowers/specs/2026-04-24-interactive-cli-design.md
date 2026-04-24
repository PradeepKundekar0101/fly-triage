# Interactive CLI + Global Install — Design Spec

## Overview

Enhance the fly-triage CLI with interactive startup prompts, a post-diagnosis menu for exploration, and global installability via `make install`.

## 1. Global Install

**Makefile** at project root with targets:
- `build`: Compiles Go binary + TypeScript agent
- `install`: Builds, then copies binary to `/usr/local/bin/fly-triage`
- `generate`: Runs the mock telemetry generator

The Go binary is built from `cli/` and the Node agent is compiled from `agent/`. The install target places the binary in PATH so users can run `fly-triage` from anywhere.

**Important:** The Go CLI resolves the agent path (`agent/dist/agent.js`) relative to the current working directory. This means the user must run `fly-triage` from the project root, or we embed the project path at build time. For the MVP, we document that users should run from the project directory. The Makefile `install` target will embed the project directory path as a build-time variable using `-ldflags`.

## 2. Interactive Startup Mode

When run **without** `--file`, the CLI enters interactive mode with stdin prompts.

**Prompts (in order):**

1. **File path** (required):
   ```
   Enter path to telemetry file:
   ```
   - Validates file exists before proceeding
   - If invalid, prints error and re-prompts

2. **Severity filter** (optional, default "all"):
   ```
   Severity filter [all/warning/error] (default: all):
   ```
   - `all`: keeps warning + error lines (current behavior)
   - `error`: keeps only error lines, drops warnings
   - Empty input → defaults to `all`

3. **Machine ID filter** (optional):
   ```
   Focus on specific machine ID? (leave blank for all):
   ```
   - If provided, only passes logs matching that machine_id to the agent
   - Empty input → no filtering (all machines)

**Non-interactive mode preserved:** When `--file` flag is provided, all prompts are skipped and the CLI runs with current behavior (severity=all, no machine filter). This keeps the tool scriptable.

## 3. Filtering Enhancements

The Go CLI filtering logic is extended:

- Current: skip lines containing `"level":"info"`
- New: if severity filter is `error`, also skip lines containing `"level":"warning"`
- New: if machine ID filter is set, after level filtering, also skip lines where the line does not contain `"machine_id":"<value>"` (same fast string-contains approach, no JSON parsing)

## 4. Post-Diagnosis Interactive Menu

After printing diagnoses, instead of exiting, the CLI shows a menu loop:

```
━━━ What next? ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  [1] Drill into a machine (show error timeline)
  [2] Ask a follow-up question (uses AI)
  [3] Export diagnosis to JSON file
  [4] Re-run with different filters
  [q] Quit

  Choice:
```

### Option 1: Drill into machine

- Prompts: `Machine ID: `
- Reads the temp filtered JSON file (already written during analysis)
- Parses the JSON array, filters entries matching the machine_id
- Prints a formatted chronological timeline:
  ```
  ━━━ Timeline: m_8x4k2n ━━━━━━━━━━━━━━━━━━━

    10:20:00  [WARNING] [nvme]      NVMe read timeout on /dev/nvme0n1 — latency 12400ms
    10:20:05  [ERROR]   [nvme]      NVMe device /dev/nvme0n1 not responding — aborting pending I/O
    10:20:12  [ERROR]   [flyd]      Health check failed for machine m_8x4k2n — volume read error
    10:20:20  [ERROR]   [flyd]      Machine m_8x4k2n entered state 'failed' — cannot transition from 'starting'

  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  ```
- Color-coded: warnings yellow, errors red
- Returns to menu after display

### Option 2: Ask a follow-up question

- Prompts: `Question: `
- Invokes the Node agent in follow-up mode: `node agent/dist/agent.js --followup <tmpfile> "<question>"`
- The agent loads the filtered logs, sends the question to Claude with the logs as context, and prints a plain-text answer to stdout
- The Go CLI captures and prints the response
- Returns to menu

### Option 3: Export diagnosis to JSON

- Prompts: `Output file path (default: diagnosis.json): `
- Writes the diagnosis JSON array (already captured from the agent's stdout) to the specified file
- Prints confirmation: `Exported to diagnosis.json`
- Returns to menu

### Option 4: Re-run with different filters

- Loops back to the startup prompts (file path, severity, machine filter)
- Re-runs the full pipeline with new filters

### q: Quit

- Prints farewell and exits cleanly

**Non-interactive mode:** When `--file` is provided (non-interactive), the menu is skipped entirely. The CLI prints diagnoses and exits, preserving scriptability.

## 5. Agent Follow-up Mode

**File:** `agent/src/agent.ts`

New CLI interface when `--followup` flag is present:

```
node agent/dist/agent.js --followup <filtered-logs.json> <question>
```

**Behavior:**
- Parse args: detect `--followup` as `process.argv[2]`, file path as `process.argv[3]`, question as `process.argv[4]`
- Read the filtered logs JSON file
- Send a single Claude call with:
  - System prompt: same Fly.io domain knowledge
  - User prompt: the filtered error logs + the user's question
  - Instruction to respond in plain text (not JSON)
- Print the plain-text response to stdout

**Output:** Plain text to stdout (not JSON). The Go CLI prints it directly.

## 6. Makefile

```makefile
PROJECT_DIR := $(shell pwd)

.PHONY: build install generate clean

build:
	cd agent && npm run build
	cd cli && go build -ldflags "-X main.projectDir=$(PROJECT_DIR)" -o ../fly-triage .

install: build
	cp fly-triage /usr/local/bin/fly-triage

generate:
	cd agent && npx tsx src/generate.ts

clean:
	rm -f fly-triage
	rm -rf agent/dist tmp/filtered_*.json
```

The `-ldflags` injects `projectDir` so the installed binary knows where to find `agent/dist/agent.js` regardless of the user's current working directory.

## 7. Go CLI: projectDir Variable

Add a package-level variable in `cli/main.go`:

```go
var projectDir string // set via -ldflags at build time
```

When resolving the agent path:
- If `projectDir` is set (installed via `make install`), use `filepath.Join(projectDir, "agent", "dist", "agent.js")`
- If empty (running as `./fly-triage`), use the current relative path `agent/dist/agent.js` (current behavior)

Same logic for the temp directory: use `filepath.Join(projectDir, "tmp")` if projectDir is set.

## Files Changed

- **Modify:** `cli/main.go` — add interactive prompts, severity/machine filtering, menu loop, follow-up subprocess, projectDir variable
- **Modify:** `agent/src/agent.ts` — add `--followup` mode
- **Create:** `Makefile` — build, install, generate, clean targets

## Out of Scope

- TUI libraries or fancy terminal UI
- Config files
- Shell completion
- Auto-detection of log files
