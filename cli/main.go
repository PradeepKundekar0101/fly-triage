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

// Set via -ldflags at build time for global install
var projectDir string

func resolvePath(relative string) string {
	if projectDir != "" {
		return filepath.Join(projectDir, relative)
	}
	return relative
}

type Diagnosis struct {
	MachineID          string   `json:"machine_id"`
	HostID             string   `json:"host_id"`
	RootCause          string   `json:"root_cause"`
	Severity           string   `json:"severity"`
	AffectedComponents []string `json:"affected_components"`
	RecommendedAction  string   `json:"recommended_action"`
}

type LogEntry struct {
	Timestamp string `json:"timestamp"`
	MachineID string `json:"machine_id"`
	HostID    string `json:"host_id"`
	Level     string `json:"level"`
	Source    string `json:"source"`
	Message   string `json:"message"`
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

func promptInput(reader *bufio.Reader, prompt string) string {
	fmt.Print(prompt)
	input, _ := reader.ReadString('\n')
	return strings.TrimSpace(input)
}

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

		// Filter by level — always skip info
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
