export type DeviceStatus = "online" | "degraded" | "offline";
export type AgentStatus = "running" | "standby" | "paused";
export type WorkflowStatus = "executing" | "idle" | "blocked";
export type EventTone = "info" | "success" | "warning";

export interface MockDevice {
  id: string;
  name: string;
  model: string;
  platform: string;
  status: DeviceStatus;
  agentStatus: AgentStatus;
  workflowStatus: WorkflowStatus;
  battery: number;
  activity: string;
  workflow: string;
  lastSeen: string;
  location: string;
}

export interface MockEvent {
  id: string;
  deviceId: string;
  time: string;
  title: string;
  detail: string;
  tone: EventTone;
}

export const mockDevices: readonly MockDevice[] = [
  {
    id: "device-alpha",
    name: "Alpha",
    model: "Atlas One",
    platform: "Android 14",
    status: "online",
    agentStatus: "running",
    workflowStatus: "executing",
    battery: 82,
    activity: "Inbox triage",
    workflow: "Morning readiness",
    lastSeen: "12 sec ago",
    location: "Lab A",
  },
  {
    id: "device-bravo",
    name: "Bravo",
    model: "Atlas One",
    platform: "Android 14",
    status: "online",
    agentStatus: "standby",
    workflowStatus: "idle",
    battery: 64,
    activity: "Awaiting assignment",
    workflow: "No active workflow",
    lastSeen: "18 sec ago",
    location: "Lab A",
  },
  {
    id: "device-charlie",
    name: "Charlie",
    model: "Vector X",
    platform: "Android 13",
    status: "degraded",
    agentStatus: "running",
    workflowStatus: "blocked",
    battery: 31,
    activity: "Recovering session",
    workflow: "Account review",
    lastSeen: "47 sec ago",
    location: "Lab B",
  },
  {
    id: "device-delta",
    name: "Delta",
    model: "Vector X",
    platform: "Android 13",
    status: "offline",
    agentStatus: "paused",
    workflowStatus: "idle",
    battery: 12,
    activity: "Connection lost",
    workflow: "No active workflow",
    lastSeen: "8 min ago",
    location: "Lab B",
  },
  {
    id: "device-echo",
    name: "Echo",
    model: "Orbit Mini",
    platform: "Android 12",
    status: "online",
    agentStatus: "running",
    workflowStatus: "executing",
    battery: 91,
    activity: "Verifying screen",
    workflow: "Regression sweep",
    lastSeen: "5 sec ago",
    location: "Lab C",
  },
  {
    id: "device-foxtrot",
    name: "Foxtrot",
    model: "Orbit Mini",
    platform: "Android 12",
    status: "online",
    agentStatus: "standby",
    workflowStatus: "idle",
    battery: 76,
    activity: "Ready",
    workflow: "No active workflow",
    lastSeen: "21 sec ago",
    location: "Lab C",
  },
];

export const mockEvents: readonly MockEvent[] = [
  {
    id: "event-001",
    deviceId: "device-alpha",
    time: "09:42:18",
    title: "Workflow checkpoint reached",
    detail: "Inbox triage completed screen verification.",
    tone: "success",
  },
  {
    id: "event-002",
    deviceId: "device-alpha",
    time: "09:41:54",
    title: "Agent observation updated",
    detail: "Foreground surface is stable and actionable.",
    tone: "info",
  },
  {
    id: "event-003",
    deviceId: "device-alpha",
    time: "09:40:27",
    title: "Workflow started",
    detail: "Morning readiness entered execution.",
    tone: "info",
  },
  {
    id: "event-004",
    deviceId: "device-charlie",
    time: "09:39:06",
    title: "Recovery required",
    detail: "The session boundary needs operator review.",
    tone: "warning",
  },
  {
    id: "event-005",
    deviceId: "device-echo",
    time: "09:38:42",
    title: "Screen verification passed",
    detail: "Regression sweep advanced to the next checkpoint.",
    tone: "success",
  },
];

export const mockSummary = {
  totalDevices: mockDevices.length,
  onlineDevices: mockDevices.filter((device) => device.status === "online").length,
  activeAgents: mockDevices.filter((device) => device.agentStatus === "running").length,
  activeWorkflows: mockDevices.filter((device) => device.workflowStatus === "executing").length,
} as const;
