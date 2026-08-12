import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  createWebhookSubscription,
  listWebhookDeliveries,
  listWebhookSubscriptions,
  replayWebhookDelivery,
} from "../client";
import type { WebhookDelivery, WebhookSubscription } from "../client";
import { PreferencesProvider } from "../lib/preferences";
import { WebhookDeliveriesPanel } from "./WebhookDeliveriesPanel";

vi.mock("../client", () => ({
  createWebhookSubscription: vi.fn(),
  listWebhookDeliveries: vi.fn(),
  listWebhookSubscriptions: vi.fn(),
  replayWebhookDelivery: vi.fn(),
  updateWebhookSubscription: vi.fn(),
}));

const mockCreate = vi.mocked(createWebhookSubscription);
const mockListDeliveries = vi.mocked(listWebhookDeliveries);
const mockListSubscriptions = vi.mocked(listWebhookSubscriptions);
const mockReplay = vi.mocked(replayWebhookDelivery);

const subscription: WebhookSubscription = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "Security automation",
  endpointUrl: "https://hooks.example.test/artifacts",
  eventTypes: ["artifact.quarantined", "artifact.released"],
  enabled: true,
  version: "1",
  secretConfigured: true,
  createdAt: "2026-08-12T01:00:00Z",
  updatedAt: "2026-08-12T01:00:00Z",
};

const deadDelivery: WebhookDelivery = {
  id: "22222222-2222-4222-8222-222222222222",
  eventId: "33333333-3333-4333-8333-333333333333",
  eventType: "artifact.quarantined",
  subscriptionId: subscription.id,
  state: "dead",
  attempts: 8,
  lastStatus: 503,
  lastError: "receiver unavailable",
  createdAt: "2026-08-12T01:01:00Z",
  updatedAt: "2026-08-12T01:02:00Z",
};

const renderPanel = () =>
  render(
    <PreferencesProvider>
      <WebhookDeliveriesPanel />
    </PreferencesProvider>,
  );

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("WebhookDeliveriesPanel", () => {
  it("shows delivery failures and explicitly replays dead deliveries", async () => {
    const user = userEvent.setup();
    mockListSubscriptions.mockResolvedValue({ data: [subscription] } as never);
    mockListDeliveries.mockResolvedValue({ data: [deadDelivery] } as never);
    mockReplay.mockResolvedValue({
      data: { ...deadDelivery, state: "pending", attempts: 0 },
    } as never);

    renderPanel();

    expect(
      (await screen.findAllByText("Security automation"))[0],
    ).toBeInTheDocument();
    expect(screen.getByText("receiver unavailable")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重放投递" }));

    expect(mockReplay).toHaveBeenCalledWith({
      path: { deliveryId: deadDelivery.id },
    });
    expect(mockListDeliveries).toHaveBeenCalledTimes(2);
  });

  it("creates a signed subscription with the supported event set", async () => {
    const user = userEvent.setup();
    mockListSubscriptions.mockResolvedValue({ data: [] } as never);
    mockListDeliveries.mockResolvedValue({ data: [] } as never);
    mockCreate.mockResolvedValue({ data: subscription } as never);

    renderPanel();
    await screen.findByText("还没有 Webhook 订阅");
    await user.click(screen.getAllByRole("button", { name: "新建订阅" })[0]);
    await user.type(
      screen.getByPlaceholderText("例如：安全自动化"),
      "Security automation",
    );
    await user.type(
      screen.getByPlaceholderText("https://hooks.example.com/artifacts"),
      "https://hooks.example.test/artifacts",
    );
    await user.type(
      screen.getByPlaceholderText("至少 32 个字符"),
      "webhook-signing-secret-at-least-32-characters",
    );
    await user.click(screen.getByRole("button", { name: "创建" }));

    expect(mockCreate).toHaveBeenCalledWith({
      body: {
        name: "Security automation",
        endpointUrl: "https://hooks.example.test/artifacts",
        secret: "webhook-signing-secret-at-least-32-characters",
        eventTypes: ["artifact.quarantined", "artifact.released"],
        enabled: true,
      },
    });
  });
});
