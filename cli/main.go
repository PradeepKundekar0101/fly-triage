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
	anomalyCount := totalLines - infoLines

	// Print filtering stats
	fmt.Printf("\n%s━━━ fly-triage ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n\n", colorBold, colorReset)
	fmt.Printf("  Processed: %s%d%s lines in %s\n", colorBold, totalLines, colorReset, filterDuration.Round(time.Millisecond))
	fmt.Printf("  Filtered:  %s%d%s info lines removed\n", colorDim, infoLines, colorReset)
	fmt.Printf("  Anomalies: %s%d%s warning/error lines\n", colorBold, anomalyCount, colorReset)
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
