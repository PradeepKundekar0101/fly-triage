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
