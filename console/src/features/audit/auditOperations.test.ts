import { describe, expect, it } from "vitest";
import { auditOperationLabel } from "./auditOperations";

const zh = (chinese: string) => chinese;
const en = (_chinese: string, english: string) => english;

describe("auditOperationLabel", () => {
  it("translates the traffic operations derived from HTTP methods", () => {
    expect(auditOperationLabel("get", zh)).toBe("读取（GET）");
    expect(auditOperationLabel("put", en)).toBe("Upload (PUT)");
    expect(auditOperationLabel("delete", zh)).toBe("删除（DELETE）");
  });

  it("translates every management code family", () => {
    const samples: [string, string, string][] = [
      ["repository.grants.upsert", "写入仓库授权", "Upsert repository grant"],
      ["repository.grants.delete", "删除仓库授权", "Delete repository grant"],
      ["artifact.tombstone", "墓碑化制品", "Tombstone artifact"],
      ["user.login", "用户登录", "User sign-in"],
      ["promote", "晋升制品", "Promote artifact"],
      [
        "apt.repository_snapshot.publish",
        "发布 APT 快照",
        "Publish APT snapshot",
      ],
      [
        "webhook.subscription.create",
        "创建 Webhook 订阅",
        "Create webhook subscription",
      ],
      [
        "lifecycle.intelligence_reconcile",
        "对账制品情报",
        "Reconcile artifact intelligence",
      ],
      ["capacity.configure", "配置仓库容量", "Configure capacity"],
      [
        "service_account.credential.revoke",
        "吊销服务账号凭据",
        "Revoke service account credential",
      ],
    ];
    for (const [code, chinese, english] of samples) {
      expect(auditOperationLabel(code, zh)).toBe(chinese);
      expect(auditOperationLabel(code, en)).toBe(english);
    }
  });

  it("keeps an unknown code readable by showing the code itself", () => {
    expect(auditOperationLabel("future.feature.action", zh)).toBe(
      "future.feature.action",
    );
    expect(auditOperationLabel("future.feature.action", en)).toBe(
      "future.feature.action",
    );
  });
});
