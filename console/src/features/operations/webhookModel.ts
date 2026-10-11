import type { WebhookDeliveryState, WebhookEventType } from "../../client";

export type SubscriptionForm = {
  name: string;
  endpointUrl: string;
  secret: string;
  eventTypes: WebhookEventType[];
  enabled: boolean;
};

export const DEFAULT_FORM: SubscriptionForm = {
  name: "",
  endpointUrl: "",
  secret: "",
  eventTypes: ["artifact.quarantined", "artifact.released"],
  enabled: true,
};

export const DELIVERY_STATES: WebhookDeliveryState[] = [
  "pending",
  "delivering",
  "retrying",
  "succeeded",
  "dead",
];
