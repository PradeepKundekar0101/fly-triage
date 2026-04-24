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
