import type { MessageKey } from "../../messages";
import type { Translate } from "../../lib/preferences";

const AUDIT_OPERATION_LABELS: Record<string, MessageKey> = {
  // Artifact traffic, derived from the HTTP method or the protocol handler.
  get: "audit.readGET",
  head: "audit.probeHEAD",
  put: "audit.uploadPUT",
  post: "audit.submitPOST",
  delete: "audit.deleteDELETE",
  patch: "audit.modifyPATCH",
  commit: "audit.commitDeployment",
  // Repository operations recorded for denied management requests. The values
  // come from the authorization layer's operation enum, so all four can appear.
  read: "audit.read",
  write: "audit.write",
  admin: "audit.repositoryAdmin",
  intelligence: "audit.artifactIntelligence",
  // Distribution, recorded as the operation itself or with a refusal suffix.
  promote: "audit.promoteArtifact",
  "promote.quarantine": "audit.promotionBlockedByQuarantine",
  "promote.security_policy": "audit.promotionBlockedBySecurityPolicy",
  replicate: "audit.replicateArtifact",
  "replicate.quarantine": "audit.replicationBlockedByQuarantine",
  // Lifecycle and reconciliation.
  "lifecycle.run_now": "audit.runLifecycleJob",
  "lifecycle.retry": "audit.retryLifecycleJob",
  "lifecycle.cancel": "audit.cancelLifecycleJob",
  "lifecycle.intelligence_reconcile": "audit.reconcileArtifactIntelligence",
  // Historical, kept for rows written by earlier revisions or named after a
  // lifecycle job kind: the current code records no audit with these.
  grant: "audit.grant",
  promotion: "audit.promotionJob",
  scan: "audit.scanJob",
  lifecycle: "audit.lifecycleJob",
  "intelligence-copy": "audit.copyArtifactIntelligence",
  publish: "audit.publish",
  browse: "audit.browse",
  restore: "audit.restore",
  retain: "audit.retain",
  reclaim: "audit.reclaim",
  // Repository grants and authorization.
  "repository.grants.replace": "audit.replaceRepositoryGrants",
  "repository.grants.apply_template": "audit.applyAuthorizationTemplate",
  "repository.grants.upsert": "audit.upsertRepositoryGrant",
  "repository.grants.delete": "audit.deleteRepositoryGrant",
  "authorization_role.create": "audit.createRole",
  "authorization_role.update": "audit.updateRole",
  "authorization_role.delete": "audit.deleteRole",
  "authorization_template.create": "audit.createAuthorizationTemplate",
  "authorization_template.update": "audit.updateAuthorizationTemplate",
  "authorization_template.delete": "audit.deleteAuthorizationTemplate",
  // Artifacts.
  "artifact.tombstone": "audit.tombstoneArtifact",
  "artifact.quarantine": "audit.quarantineArtifact",
  "artifact.release": "audit.releaseArtifact",
  "artifact.intelligence.replace": "audit.replaceArtifactIntelligence",
  "artifact.scan.enqueue": "audit.enqueueArtifactScan",
  "artifact.scan.auto_enqueue": "audit.autoEnqueueArtifactScan",
  "artifact.scan.reconcile": "audit.reconcileScan",
  "conan.package_revision.tombstone": "audit.tombstoneConanPackageRevision",
  // APT publication.
  "apt.publication_session.create": "audit.createAPTPublicationSession",
  "apt.publication_package.stage": "audit.stageAPTPublicationPackage",
  "apt.repository_snapshot.publish": "audit.publishAPTSnapshot",
  "apt.repository_snapshot.export": "audit.exportAPTSnapshot",
  "apt.repository_snapshot.restore": "audit.restoreAPTSnapshot",
  "apt.snapshot.prune": "audit.pruneAPTSnapshots",
  // Package-level APT operations on a published snapshot.
  "apt.package.promote": "audit.promoteAPTPackage",
  "apt.package.replicate": "audit.replicateAPTPackage",
  "apt.package.delete": "audit.deleteAPTPackage",
  "apt.package.restore": "audit.restoreAPTPackage",
  "apt.package.retention": "audit.retainAPTPackage",
  // Authentication and users.
  "authentication.oidc.configure": "audit.configureOIDC",
  "authentication.oidc.test": "audit.testOIDC",
  "user.login": "audit.userSignIn",
  "user.password.change": "audit.changePassword",
  "user.password.reset": "audit.resetPassword",
  "user.create": "audit.createUser",
  "user.update": "audit.updateUser",
  "user.delete": "audit.deleteUser",
  "user.sessions.revoke": "audit.revokeAllSessions",
  "user.session.list": "audit.listUserSessions",
  "user.session.revoke": "audit.revokeSession",
  "user.identity.link": "audit.linkIdentity",
  "user.identity.unlink": "audit.unlinkIdentity",
  // Service accounts.
  "service_account.create": "audit.createServiceAccount",
  "service_account.update": "audit.updateServiceAccount",
  "service_account.credential.create": "audit.createServiceAccountCredential",
  "service_account.credential.revoke": "audit.revokeServiceAccountCredential",
  // Policies and settings.
  "capacity.configure": "audit.configureCapacity",
  "quarantine_read_policy.replace": "audit.replaceQuarantineReadPolicy",
  "security_policy.replace": "audit.replaceSecurityPolicy",
  "security_policy.evaluate": "audit.evaluateSecurityPolicy",
  "site-settings.replace": "audit.updateSiteSettings",
  "console-theme.install": "audit.installTheme",
  "console-theme.replace": "audit.replaceTheme",
  "console-theme.delete": "audit.deleteTheme",
  // Groups, webhooks, and scheduled tasks.
  "group.membership_denied": "audit.groupMembershipDenied",
  "webhook.subscription.create": "audit.createWebhookSubscription",
  "webhook.subscription.update": "audit.updateWebhookSubscription",
  "webhook.delivery.replay": "audit.replayWebhookDelivery",
  "scheduled_task.create": "audit.createScheduledTask",
  "scheduled_task.update": "audit.updateScheduledTask",
  "scheduled_task.delete": "audit.deleteScheduledTask",
  "scheduled_task.run": "audit.runScheduledTask",
  "scheduled_task.run_failed": "audit.scheduledTaskRunFailed",
};

/**
 * The localized label for an audit operation code, or the raw code when the
 * map does not know it.
 */
export function auditOperationLabel(value: string, t: Translate): string {
  const label = AUDIT_OPERATION_LABELS[value];
  return label ? t(label) : value;
}
