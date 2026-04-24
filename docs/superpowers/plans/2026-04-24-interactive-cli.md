# Interactive CLI + Global Install Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add interactive prompts, post-diagnosis menu with drill-down/follow-up/export, and `make install` for global `fly-triage` command.

**Architecture:** Go CLI gets interactive mode (no `--file` → prompts), enhanced filtering (severity + machine ID), and a post-diagnosis menu loop. Node agent gets a `--followup` mode for conversational Q&A. Makefile with `-ldflags` embeds project path for global install.

**Tech Stack:** Go stdlib (flag, bufio, os/exec, encoding/json), TypeScript, @anthropic-ai/sdk, Make

---

## File Structure

```
fly-triage/
├── cli/
│   └── main.go              # MODIFY: interactive prompts, filtering, menu loop, projectDir
├── agent/
│   └── src/
│       └── agent.ts          # MODIFY: add --followup mode
├── Makefile                  # CREATE: build, install, generate, clean
```

---

### Task 1: Makefile + projectDir Build Variable

**Files:**
- Create: `Makefile`
- Modify: `cli/main.go` (add `projectDir` variable and path resolution)

- [ ] **Step 1: Create the Makefile**

Create `Makefile` at the project root:

```makefile
PROJECT_DIR := $(shell pwd)

.PHONY: build install generate clean

build:
	cd agent && npm run build
	cd cli && go build -ldflags "-X main.projectDir=$(PROJECT_DIR)" -o ../fly-triage .

install: build
	cp fly-triage /usr/local/bin/fly-triage
	@echo "Installed fly-triage to /usr/local/bin/fly-triage"

generate:
	cd agent && npx tsx src/generate.ts

clean:
	rm -f fly-triage
	rm -rf agent/dist tmp/filtered_*.json
```

- [ ] **Step 2: Add projectDir variable and resolvePath helper to main.go**

Add these lines after the imports block and before the `Diagnosis` struct in `cli/main.go`:

```go
// Set via -ldflags at build time for global install
var projectDir string

func resolvePath(relative string) string {
	if projectDir != "" {
		return filepath.Join(projectDir, relative)
	}
	return relative
}
```

- [ ] **Step 3: Update main.go to use resolvePath for agent and tmp paths**

In the `main()` function, replace the hardcoded paths:

Replace:
```go
	tmpDir := "tmp"
```
With:
```go
	tmpDir := resolvePath("tmp")
```

Replace:
```go
	agentPath := filepath.Join("agent", "dist", "agent.js")
```
With:
```go
	agentPath := resolvePath(filepath.Join("agent", "dist", "agent.js"))
```

- [ ] **Step 4: Build and verify with make**

```bash
cd /Users/pradeepkundekar/Desktop/Projects/fly-triage && make build
```

Expected: `fly-triage` binary created. Test:

```bash
./fly-triage --file nonexistent.jsonl
# Expected: "Error: cannot open file nonexistent.jsonl: ..."
```

- [ ] **Step 5: Test make install**

```bash
make install
which fly-triage
# Expected: /usr/local/bin/fly-triage
fly-triage --file nonexistent.jsonl
# Expected: same error output, proving the installed binary works
```

- [ ] **Step 6: Commit**

```bash
git add Makefile cli/main.go
git commit -m "feat: add Makefile with build/install targets and projectDir support"
```

---

### Task 2: Interactive Startup Prompts

**Files:**
- Modify: `cli/main.go`

- [ ] **Step 1: Add a promptInput helper function**

Add this function after the `resolvePath` function in `cli/main.go`:

```go
func promptInput(reader *bufio.Reader, prompt string) string {
	fmt.Print(prompt)
	input, _ := reader.ReadString('\n')
	return strings.TrimSpace(input)
}
```

- [ ] **Step 2: Add interactive startup to main()**

Replace the current flag parsing and file-not-found block (lines 45-52 of current main.go) with the following. The entire `main()` function needs to be restructured. Replace the `main()` function with:

```go
func main() {
	filePath := flag.String("file", "", "Path to JSONL telemetry file")
	flag.Parse()

	reader := bufio.NewReader(os.Stdin)
	interactive := *filePath == ""

	var severityFilter string
	var machineFilter string

	if interactive {
		// Print welcome banner
		fmt.Printf("\n%s━━━ fly-triage ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n\n", colorBold, colorReset)
		fmt.Printf("  %sAI-Powered Telemetry Diagnostic Tool%s\n\n", colorDim, colorReset)

		// Prompt for file path (required, with validation loop)
		for {
			input := promptInput(reader, "  Enter path to telemetry file: ")
			if input == "" {
				fmt.Printf("  %sFile path is required.%s\n", colorRed, colorReset)
				continue
			}
			if _, err := os.Stat(input); os.IsNotExist(err) {
				fmt.Printf("  %sFile not found: %s%s\n", colorRed, input, colorReset)
				continue
			}
			*filePath = input
			break
		}

		// Prompt for severity filter
		input := promptInput(reader, "  Severity filter [all/warning/error] (default: all): ")
		switch strings.ToLower(input) {
		case "error":
			severityFilter = "error"
		case "warning":
			severityFilter = "warning"
		default:
			severityFilter = "all"
		}

		// Prompt for machine ID filter
		machineFilter = promptInput(reader, "  Focus on specific machine ID? (leave blank for all): ")

		fmt.Println()
	} else {
		severityFilter = "all"
	}

	runAnalysis(*filePath, severityFilter, machineFilter, interactive, reader)
}
```

- [ ] **Step 3: Extract the analysis pipeline into a runAnalysis function**

Add this function that contains the existing filtering + agent invocation + pretty-print logic, but with the new severity and machine filters:

```go
func runAnalysis(filePath, severityFilter, machineFilter string, interactive bool, reader *bufio.Reader) {
	// Open and stream the file
	f, err := os.Open(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot open file %s: %v\n", filePath, err)
		os.Exit(1)
	}
	defer f.Close()

	startTime := time.Now()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var filtered []json.RawMessage
	totalLines := 0
	filteredOutLines := 0

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		totalLines++

		// Filter by level
		if strings.Contains(line, `"level":"info"`) || strings.Contains(line, `"level": "info"`) {
			filteredOutLines++
			continue
		}

		// If severity filter is "error", also skip warnings
		if severityFilter == "error" {
			if strings.Contains(line, `"level":"warning"`) || strings.Contains(line, `"level": "warning"`) {
				filteredOutLines++
				continue
			}
		}

		// Filter by machine ID if specified
		if machineFilter != "" {
			if !strings.Contains(line, `"machine_id":"`+machineFilter+`"`) && !strings.Contains(line, `"machine_id": "`+machineFilter+`"`) {
				filteredOutLines++
				continue
			}
		}

		filtered = append(filtered, json.RawMessage(line))
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	// Write filtered output to temp file
	tmpDir := resolvePath("tmp")
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
	anomalyCount := totalLines - filteredOutLines

	// Print filtering stats
	fmt.Printf("\n%s━━━ fly-triage ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n\n", colorBold, colorReset)
	fmt.Printf("  Processed: %s%d%s lines in %s\n", colorBold, totalLines, colorReset, filterDuration.Round(time.Millisecond))
	fmt.Printf("  Filtered:  %s%d%s lines removed\n", colorDim, filteredOutLines, colorReset)
	fmt.Printf("  Anomalies: %s%d%s lines to analyze\n", colorBold, anomalyCount, colorReset)
	fmt.Printf("\n  Analyzing with Claude Sonnet...\n")

	// Execute Node agent
	agentPath := resolvePath(filepath.Join("agent", "dist", "agent.js"))
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
		if interactive {
			menuLoop(reader, diagnoses, tmpFile, filePath, output)
		}
		return
	}

	// Pretty print each diagnosis
	printDiagnoses(diagnoses)

	if interactive {
		menuLoop(reader, diagnoses, tmpFile, filePath, output)
	}
}
```

- [ ] **Step 4: Extract printDiagnoses helper**

Add this function:

```go
func printDiagnoses(diagnoses []Diagnosis) {
	for _, d := range diagnoses {
		color := severityColor(d.Severity)
		fmt.Printf("\n%s━━━ Diagnosis ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n\n", colorBold, colorReset)
		fmt.Printf("  Machine:  %s%s%s\n", color, d.MachineID, colorReset)
		fmt.Printf("  Host:     %s\n", d.HostID)
		fmt.Printf("  Severity: %s%s%s\n", color, d.Severity, colorReset)
		fmt.Printf("  Components: %s\n", strings.Join(d.AffectedComponents, ", "))
		fmt.Printf("\n  %sCause:%s\n", colorBold, colorReset)
		for _, line := range wordWrap(d.RootCause, 60) {
			fmt.Printf("    %s\n", line)
		}
		fmt.Printf("\n  %sAction:%s\n", colorBold, colorReset)
		for _, line := range strings.Split(d.RecommendedAction, "\n") {
			fmt.Printf("    %s%s%s\n", colorGreen, line, colorReset)
		}
	}
	fmt.Printf("\n%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n", colorDim, colorReset)
}
```

- [ ] **Step 5: Build and test interactive mode**

```bash
cd /Users/pradeepkundekar/Desktop/Projects/fly-triage && make build
./fly-triage
# Should show welcome banner and prompt for file path
# Type a nonexistent file → should re-prompt
# Type machine_events.jsonl → should proceed to severity prompt
# Press enter for defaults → should start analysis (will fail at agent without API key, that's OK)
```

Also test non-interactive mode still works:

```bash
./fly-triage --file machine_events.jsonl 2>&1 | head -10
# Should show stats without prompts
```

- [ ] **Step 6: Commit**

```bash
git add cli/main.go
git commit -m "feat: add interactive startup prompts with severity and machine filters"
```

---

### Task 3: Post-Diagnosis Interactive Menu

**Files:**
- Modify: `cli/main.go`

- [ ] **Step 1: Add the menuLoop function**

Add this function to `cli/main.go`:

```go
func menuLoop(reader *bufio.Reader, diagnoses []Diagnosis, tmpFile, originalFile string, rawOutput []byte) {
	for {
		fmt.Printf("\n%s━━━ What next? ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n\n", colorBold, colorReset)
		fmt.Printf("  [1] Drill into a machine (show error timeline)\n")
		fmt.Printf("  [2] Ask a follow-up question (uses AI)\n")
		fmt.Printf("  [3] Export diagnosis to JSON file\n")
		fmt.Printf("  [4] Re-run with different filters\n")
		fmt.Printf("  [q] Quit\n\n")

		choice := promptInput(reader, "  Choice: ")

		switch strings.ToLower(choice) {
		case "1":
			drillIntoMachine(reader, tmpFile)
		case "2":
			askFollowUp(reader, tmpFile)
		case "3":
			exportDiagnosis(reader, rawOutput)
		case "4":
			// Re-run with new prompts
			fmt.Println()
			var filePath string
			for {
				input := promptInput(reader, "  Enter path to telemetry file: ")
				if input == "" {
					fmt.Printf("  %sFile path is required.%s\n", colorRed, colorReset)
					continue
				}
				if _, err := os.Stat(input); os.IsNotExist(err) {
					fmt.Printf("  %sFile not found: %s%s\n", colorRed, input, colorReset)
					continue
				}
				filePath = input
				break
			}
			input := promptInput(reader, "  Severity filter [all/warning/error] (default: all): ")
			var severityFilter string
			switch strings.ToLower(input) {
			case "error":
				severityFilter = "error"
			case "warning":
				severityFilter = "warning"
			default:
				severityFilter = "all"
			}
			machineFilter := promptInput(reader, "  Focus on specific machine ID? (leave blank for all): ")
			fmt.Println()
			runAnalysis(filePath, severityFilter, machineFilter, true, reader)
			return // runAnalysis will start its own menu loop
		case "q", "quit", "exit":
			fmt.Printf("\n  %sGoodbye!%s\n\n", colorDim, colorReset)
			return
		default:
			fmt.Printf("  %sInvalid choice. Enter 1-4 or q.%s\n", colorRed, colorReset)
		}
	}
}
```

- [ ] **Step 2: Add the drillIntoMachine function**

```go
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	MachineID string `json:"machine_id"`
	HostID    string `json:"host_id"`
	Level     string `json:"level"`
	Source    string `json:"source"`
	Message   string `json:"message"`
}

func drillIntoMachine(reader *bufio.Reader, tmpFile string) {
	machineID := promptInput(reader, "\n  Machine ID: ")
	if machineID == "" {
		fmt.Printf("  %sMachine ID is required.%s\n", colorRed, colorReset)
		return
	}

	// Read the filtered logs
	data, err := os.ReadFile(tmpFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Error reading filtered logs: %v\n", err)
		return
	}

	var entries []LogEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		fmt.Fprintf(os.Stderr, "  Error parsing filtered logs: %v\n", err)
		return
	}

	// Filter for this machine
	var matched []LogEntry
	for _, e := range entries {
		if e.MachineID == machineID {
			matched = append(matched, e)
		}
	}

	if len(matched) == 0 {
		fmt.Printf("  %sNo entries found for machine %s%s\n", colorYellow, machineID, colorReset)
		return
	}

	fmt.Printf("\n%s━━━ Timeline: %s ━━━━━━━━━━━━━━━━━━━%s\n\n", colorBold, machineID, colorReset)
	for _, e := range matched {
		// Extract time portion from timestamp
		t, err := time.Parse(time.RFC3339, e.Timestamp)
		timeStr := e.Timestamp
		if err == nil {
			timeStr = t.Format("15:04:05")
		}

		levelColor := colorYellow
		levelLabel := "WARNING"
		if e.Level == "error" {
			levelColor = colorRed
			levelLabel = "ERROR  "
		}

		fmt.Printf("    %s  %s[%s]%s [%-10s] %s\n", timeStr, levelColor, levelLabel, colorReset, e.Source, e.Message)
	}
	fmt.Printf("\n%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n", colorDim, colorReset)
}
```

- [ ] **Step 3: Add the exportDiagnosis function**

```go
func exportDiagnosis(reader *bufio.Reader, rawOutput []byte) {
	path := promptInput(reader, "\n  Output file path (default: diagnosis.json): ")
	if path == "" {
		path = "diagnosis.json"
	}

	if err := os.WriteFile(path, rawOutput, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "  Error writing file: %v\n", err)
		return
	}

	fmt.Printf("  %sExported to %s%s\n", colorGreen, path, colorReset)
}
```

- [ ] **Step 4: Add a stub askFollowUp function (will be completed in Task 4)**

```go
func askFollowUp(reader *bufio.Reader, tmpFile string) {
	question := promptInput(reader, "\n  Question: ")
	if question == "" {
		fmt.Printf("  %sQuestion is required.%s\n", colorRed, colorReset)
		return
	}

	fmt.Printf("\n  Asking Claude...\n\n")

	agentPath := resolvePath(filepath.Join("agent", "dist", "agent.js"))
	cmd := exec.Command("node", agentPath, "--followup", tmpFile, question)
	cmd.Stderr = os.Stderr

	output, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Error running follow-up: %v\n", err)
		return
	}

	// Print the plain-text response
	fmt.Printf("  %s━━━ Answer ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n\n", colorBold, colorReset)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fmt.Printf("    %s\n", line)
	}
	fmt.Printf("\n  %s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n", colorDim, colorReset)
}
```

- [ ] **Step 5: Build and test the menu**

```bash
cd /Users/pradeepkundekar/Desktop/Projects/fly-triage && make build
```

Expected: compiles without errors. The menu loop will be reachable after a successful analysis, or can be tested by temporarily passing empty diagnoses.

- [ ] **Step 6: Commit**

```bash
git add cli/main.go
git commit -m "feat: add post-diagnosis interactive menu with drill-down, export, and re-run"
```

---

### Task 4: Agent Follow-up Mode

**Files:**
- Modify: `agent/src/agent.ts`

- [ ] **Step 1: Add the followup handler to agent.ts**

Replace the `main()` function and the `main().catch()` call at the bottom of `agent/src/agent.ts` with:

```typescript
async function followup(filePath: string, question: string): Promise<void> {
  const client = new Anthropic();
  const raw = readFileSync(filePath, "utf-8");
  const entries: LogEntry[] = JSON.parse(raw);

  const logText = entries
    .map((e) => `[${e.timestamp}] [${e.level}] [${e.source}] [${e.machine_id}] ${e.message}`)
    .join("\n");

  const response = await client.messages.create({
    model: "claude-sonnet-4-20250514",
    max_tokens: 1024,
    system: SYSTEM_PROMPT,
    messages: [
      {
        role: "user",
        content: `Here are the error/warning logs from a Fly.io telemetry dump:

${logText}

Based on these logs, answer this question:
${question}

Respond in plain text. Be specific and reference machine IDs, hosts, and timestamps where relevant.`,
      },
    ],
  });

  const text = response.content[0].type === "text" ? response.content[0].text : "";
  console.log(text);
}

async function main(): Promise<void> {
  // Check for --followup mode
  if (process.argv[2] === "--followup") {
    const filePath = process.argv[3];
    const question = process.argv[4];
    if (!filePath || !question) {
      console.error("Usage: node agent.js --followup <filtered-logs.json> <question>");
      process.exit(1);
    }
    await followup(filePath, question);
    return;
  }

  // Normal diagnosis mode
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

  console.log(JSON.stringify(diagnoses, null, 2));
}

main().catch((err) => {
  console.error("Agent error:", err.message);
  process.exit(1);
});
```

- [ ] **Step 2: Build the agent**

```bash
cd /Users/pradeepkundekar/Desktop/Projects/fly-triage/agent && npm run build
```

Expected: compiles without errors, `dist/agent.js` updated.

- [ ] **Step 3: Test the followup mode argument parsing**

```bash
cd /Users/pradeepkundekar/Desktop/Projects/fly-triage && node agent/dist/agent.js --followup 2>&1
# Expected: "Usage: node agent.js --followup <filtered-logs.json> <question>"
```

- [ ] **Step 4: Commit**

```bash
git add agent/src/agent.ts
git commit -m "feat: add --followup mode to agent for conversational Q&A"
```

---

### Task 5: Full Build + End-to-End Verification

**Files:** No new files. Uses everything built in Tasks 1-4.

- [ ] **Step 1: Full build**

```bash
cd /Users/pradeepkundekar/Desktop/Projects/fly-triage && make build
```

Expected: Both `agent/dist/agent.js` and `fly-triage` binary built successfully.

- [ ] **Step 2: Generate fresh test data**

```bash
make generate
mv agent/machine_events.jsonl .
```

Expected: `machine_events.jsonl` created with ~7000+ lines.

- [ ] **Step 3: Test non-interactive mode (backward compatibility)**

```bash
./fly-triage --file machine_events.jsonl 2>&1 | head -10
```

Expected: Shows stats (processed, filtered, anomalies), then attempts agent (will fail without API key). Should NOT show prompts or menu.

- [ ] **Step 4: Test interactive mode startup**

```bash
echo -e "machine_events.jsonl\n\n\n" | ./fly-triage 2>&1 | head -20
```

Expected: Shows welcome banner, accepts file path, defaults severity to all and machine filter to blank, starts processing.

- [ ] **Step 5: Test make install**

```bash
make install
which fly-triage
# Expected: /usr/local/bin/fly-triage
fly-triage --file machine_events.jsonl 2>&1 | head -8
# Expected: shows stats, proving the global command works with embedded projectDir
```

- [ ] **Step 6: Rebuild agent and verify full pipeline (requires API key)**

If `ANTHROPIC_API_KEY` is set:

```bash
fly-triage --file machine_events.jsonl
```

Expected full output:
1. Stats: ~7000+ lines processed, ~8 anomalies
2. Two diagnosis blocks (NVMe + network partition)
3. Interactive menu appears after diagnoses
4. Option 1 (drill into m_8x4k2n) shows timeline
5. Option 3 exports to diagnosis.json
6. Option q exits cleanly

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "chore: verify end-to-end interactive CLI pipeline"
```
