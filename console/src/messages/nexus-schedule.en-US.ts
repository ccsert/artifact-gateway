import type { NexusScheduleMessageKey } from "./nexus-schedule.zh-CN";

export const nexusScheduleEnUS: Record<NexusScheduleMessageKey, string> = {
  "nexusSchedule.title": "Recurring migration plans",
  "nexusSchedule.new": "Create recurring plan",
  "nexusSchedule.refresh": "Refresh plans",
  "nexusSchedule.background": "Plans run on the server",
  "nexusSchedule.backgroundHint":
    "Each occurrence fully enumerates the source and creates a durable incremental batch. Closing the browser has no effect. Missed occurrences coalesce into one, and batches never overlap.",
  "nexusSchedule.capacityBoundary":
    "Each occurrence needs a new capacity observation",
  "nexusSchedule.capacityBoundaryHint":
    "No trusted remote object-pool observer is available yet. Each administrator-reported observation authorizes one occurrence. Missing or expired observations pause dispatch until refreshed; migration is not fully unattended.",
  "nexusSchedule.empty": "No recurring plans yet",
  "nexusSchedule.emptyHint":
    "Complete a successful migration batch, then use its source version and repository mappings to create a plan.",
  "nexusSchedule.template": "Successful batch",
  "nexusSchedule.templateHint":
    "The plan freezes the batch's source version, mappings and policy. Changed bindings require a new successful batch and a new plan.",
  "nexusSchedule.interval": "Interval (minutes)",
  "nexusSchedule.createHint":
    "New plans wait for a fresh capacity observation. Once supplied, a due plan automatically dispatches one occurrence.",
  "nexusSchedule.active": "Enabled",
  "nexusSchedule.waiting_capacity": "Waiting for new capacity",
  "nexusSchedule.paused": "Paused",
  "nexusSchedule.blocked": "Needs attention",
  "nexusSchedule.stopped": "Stopped",
  "nexusSchedule.nextRun": "Next eligible occurrence",
  "nexusSchedule.lastSuccessful": "Last successful sync",
  "nexusSchedule.successScope":
    "Success covers verified native content within this plan. Regenerated Maven metadata remains in the original batch report; this does not mean the entire source is cleared or client cutover is verified.",
  "nexusSchedule.never": "No successful sync yet",
  "nexusSchedule.noNext": "Stopped; no further occurrences",
  "nexusSchedule.intervalValue": "Every {minutes} minutes",
  "nexusSchedule.currentBatch": "View active batch",
  "nexusSchedule.lastBatch": "View latest batch",
  "nexusSchedule.pause": "Pause plan",
  "nexusSchedule.resume": "Refresh capacity and resume",
  "nexusSchedule.capacity": "Supply capacity observation",
  "nexusSchedule.stop": "Stop plan",
  "nexusSchedule.stopConfirm":
    "Stopping permanently prevents new batches. The active batch and published artifacts are retained.",
  "nexusSchedule.stopHint":
    "Pause and stop affect future batches. Manage the active batch separately on its batch page.",
  "nexusSchedule.saveCapacity": "Submit new observation",
  "nexusSchedule.blockedHint":
    "Check the latest batch. Source or mapping changes require a new plan based on a new successful batch.",
  "nexusSchedule.mappingBusy":
    "A target already has a live batch. Resolve it before explicitly resuming this plan.",
  "nexusSchedule.mappingExists":
    "These targets already have an unstopped recurring plan. Manage that plan instead.",
  "nexusSchedule.templateRequired":
    "Select a completed, fully successful batch without blocking exceptions.",
  "nexusSchedule.dispatchFailed":
    "Dispatch failed. Automatic retries are limited to three; further action is then required.",
  "nexusSchedule.policyChanged":
    "The policy changed. Create a new plan based on a new successful batch.",
};
