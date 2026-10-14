import type { SystemMessageKey } from "./system.zh-CN";

export const systemEnUS: Record<SystemMessageKey, string> = {
  "system.notFiring": "Not firing",
  "system.warning": "Warning",
  "system.critical": "Critical",
  "system.recovered": "Recovered",
  "system.sampleAvailable": "Sample available",
  "system.capacityUnknown": "Capacity unknown",
  "system.sampleStale": "Sample stale",
  "system.repositoryQuotaNotConfigured": "Repository quota not configured",
  "system.configurationChanged": "Configuration changed",
  "system.disabled": "Disabled",
  "system.deletedHistoryRetained": "Deleted; history retained",
  "system.repositoryDeleted": "Repository deleted",
  "system.repositoryInactive": "Repository inactive",
  "system.pendingDelivery": "Pending delivery",
  "system.delivering": "Delivering",
  "system.retryScheduled": "Retry scheduled",
  "system.acceptedBySMTPServer": "Accepted by SMTP server",
  "system.deliveryStopped": "Delivery stopped",
  "system.queuedForDelivery": "Queued for delivery",
  "system.deliveryQueueFull": "Delivery queue full",
  "system.emailChannelDisabled": "Email channel disabled",
  "system.encryptionKeyUnavailable": "Encryption key unavailable",
  "system.emailTargetDisabled": "Email target disabled",
  "system.emailTargetVersionChanged": "Email target version changed",
  "system.emailTargetUnavailable": "Email target unavailable",
  "system.automaticDeliveryCancelledAfterRuleChange":
    "Automatic delivery cancelled after rule change",
  "system.automaticDeliveryCancelledAfterDisablingTheRule":
    "Automatic delivery cancelled after disabling the rule",
  "system.automaticDeliveryCancelledAfterDeletingTheRule":
    "Automatic delivery cancelled after deleting the rule",
  "system.networkConnectionFailed": "Network connection failed",
  "system.tLSVerificationFailed": "TLS verification failed",
  "system.sMTPServerRejectedTheMessage": "SMTP server rejected the message",
  "system.temporarySMTPFailure": "Temporary SMTP failure",
  "system.deliveryLeaseExpired": "Delivery lease expired",
  "system.retryAttemptsExhausted": "Retry attempts exhausted",
  "system.messageValidationFailed": "Message validation failed",
  "system.emailRelayResolutionFailed": "Email relay resolution failed",
  "system.emailRelayAddressNotApproved": "Email relay address not approved",
  "system.emailRelayConnectionFailed": "Email relay connection failed",
  "system.tLSConfigurationUnavailable": "TLS configuration unavailable",
  "system.requiredTLSUnavailable": "Required TLS unavailable",
  "system.emailAuthenticationConfigurationUnavailable":
    "Email authentication configuration unavailable",
  "system.emailAuthenticationFailed": "Email authentication failed",
  "system.permanentSMTPRejection": "Permanent SMTP rejection",
  "system.temporarySMTPRejection": "Temporary SMTP rejection",
  "system.sMTPTransportFailed": "SMTP transport failed",
  "system.deliveryOutcomeUnknown": "Delivery outcome unknown",
  "system.statusUnknown": "Status unknown",
  "system.anotherAdministratorChangedThisRuleGoBackRefresh":
    "Another administrator changed this rule. Go back, refresh, and edit again; the newer configuration was not overwritten.",
  "system.changeYourPasswordBeforeReturningToThisPage":
    "Change your password before returning to this page.",
  "system.permissionDeniedConfirmPlatformAdministratorAccessAndRefresh":
    "Permission denied. Confirm platform administrator access and refresh.",
  "system.thisSessionCannotPerformThisActionSignIn":
    "This session cannot perform this action. Sign in again or confirm platform administrator access.",
  "system.theSessionHasExpiredSignInAgain":
    "The session has expired. Sign in again.",
  "system.thisRepositoryAlreadyHasARuleRefreshAnd":
    "This repository already has a rule. Refresh and edit the existing rule.",
  "system.theRuleLimitHasBeenReachedIncludingRetained":
    "The rule limit has been reached, including retained deleted records.",
  "system.thisRuleWasDeletedGoBackAndRefresh":
    "This rule was deleted. Go back and refresh.",
  "system.thisRepositoryIsUnavailableForAlertingCheckIts":
    "This repository is unavailable for alerting. Check its state.",
  "system.configurationValidationFailedCheckThresholdsHoldDurationsAnd":
    "Configuration validation failed. Check thresholds, hold durations, and target, then retry.",
  "system.theResourceOrManagementEndpointIsUnavailableRefresh":
    "The resource or management endpoint is unavailable. Refresh and check the Gateway version.",
  "system.theEmailTargetWasDisabledRefreshAndChoose":
    "The email target was disabled. Refresh and choose an available target.",
  "system.theEmailChannelIsDisabledTheRuleCannot":
    "The email channel is disabled; the rule cannot be enabled.",
  "system.encryptionKeyUnavailableTheRuleCannotBeEnabled":
    "Encryption key unavailable; the rule cannot be enabled.",
  "system.requestFailedCheckTheConnectionAndRefreshBefore":
    "Request failed. Check the connection and refresh before retrying.",
  "system.unknown": "Unknown",
  "system.enterAPercentageAbove0AndAtMost":
    "Enter a percentage above 0 and at most 100, with up to two decimals.",
  "system.enterIntegerSecondsFrom186400": "Enter integer seconds from 1–86400.",
  "system.enterIntegerSecondsFrom303600": "Enter integer seconds from 30–3600.",
  "system.recoveryMustBeBelowTheWarningThreshold":
    "Recovery must be below the warning threshold.",
  "system.warningMustBeBelowTheCriticalThreshold":
    "Warning must be below the critical threshold.",
  "system.chooseARepository": "Choose a repository.",
  "system.chooseAnEmailTarget": "Choose an email target.",
  "system.editRule": "Edit rule",
  "system.createDisabledRule": "Create disabled rule",
  "system.loadMoreRepositories": "Load more repositories",
  "system.repository": "Repository",
  "system.repositoryScopeCannotBeChangedAfterCreation":
    "Repository scope cannot be changed after creation.",
  "system.chooseARepository2": "Choose a repository",
  "system.emailTarget": "Email target",
  "system.onlyTheNameAndConfigurationStatusAreShown":
    "Only the name and configuration status are shown; recipient addresses are not read.",
  "system.chooseAnEmailTarget2": "Choose an email target",
  "system.ready": "Ready",
  "system.notReady": "Not ready",
  "system.triggerWarning": "Trigger warning",
  "system.triggersAfterUsageReachesTheThresholdForThe":
    "Triggers after usage reaches the threshold for the specified duration.",
  "system.escalateToCritical": "Escalate to critical",
  "system.recovery": "Recovery",
  "system.recoveryIsNotifiedAfterUsageStaysStrictlyBelow":
    "Recovery is notified after usage stays strictly below this threshold for the specified duration.",
  "system.maximumSampleAgeSeconds": "Maximum sample age (seconds)",
  "system.staleOrUnknownEvidenceInterruptsHoldTimingAnd":
    "Stale or unknown evidence interrupts hold timing and retains existing severity.",
  "system.savingBindsTheCurrentTargetVersionResetsHold":
    "Saving binds the current target version, resets hold timing, and retains severity. Unclaimed old automatic deliveries are cancelled; a claimed delivery may finish once.",
  "system.newRulesAreDisabledByDefaultAndSend":
    "New rules are disabled by default and send no test email. Review prerequisites and explicitly enable after saving.",
  "system.thisRuleIsEnabledSavingContinuesEvaluationAnd":
    "This rule is enabled; saving continues evaluation and may produce notifications.",
  "system.saveRule": "Save rule",
  "system.cancel": "Cancel",
  "system.eventRefreshFailedShowingPreviouslyReadData":
    "Event refresh failed; showing previously read data",
  "system.latest50Events": "Latest 50 events",
  "system.refreshEvents": "Refresh events",
  "system.noAlertEventsYet": "No alert events yet",
  "system.disabledRulesAndUnknownOrStaleDataDo":
    "Disabled rules and unknown or stale data do not generate recovery events.",
  "system.eventSample": "Event sample",
  "system.sampled": "Sampled",
  "system.eventThresholds": "Event thresholds",
  "system.evidenceSince": "Evidence since",
  "system.hideDeliveryDetails": "Hide delivery details",
  "system.viewDeliveryDetails": "View delivery details",
  "system.emailDeliveryDetails": "Email delivery details",
  "system.deliveryDetailsAreOutOfDate": "Delivery details are out of date",
  "system.attempts": "Attempts",
  "system.sMTPAcceptedOnlyMeansTheServerAcceptedThe":
    "SMTP accepted only means the server accepted the message; it does not confirm inbox delivery.",
  "system.possibleDuplicate": "Possible duplicate",
  "system.anEarlierDeliveryOutcomeWasUncertainARetry":
    "An earlier delivery outcome was uncertain; a retry may duplicate the email.",
  "system.nextAttempt": "Next attempt",
  "system.acceptedAt": "Accepted at",
  "system.template": "Template",
  "system.confirmEnablingThisRule": "Confirm enabling this rule",
  "system.confirmDisablingThisRule": "Confirm disabling this rule",
  "system.confirmDeletingThisRule": "Confirm deleting this rule",
  "system.localeUnknown": "Locale unknown",
  "system.enablingMaySendEmailToThisTargetFor":
    "Enabling may send email to this target for future real quota events. Previously unqueued events are not caught up, and no test email is sent.",
  "system.disablingStopsNewEvaluationsAndRetainsSeverityAnd":
    "Disabling stops new evaluations and retains severity and history. Unclaimed automatic deliveries are cancelled; a claimed delivery may finish once.",
  "system.deletionStopsNewEvaluationsAndRetainsHistoryWithout":
    "Deletion stops new evaluations and retains history without generating a recovery event. Unclaimed automatic deliveries are cancelled; a claimed delivery may finish once.",
  "system.confirmEnable": "Confirm enable",
  "system.confirmDisable": "Confirm disable",
  "system.confirmDelete": "Confirm delete",
  "system.ruleState": "Rule state",
  "system.enabled": "Enabled",
  "system.severityIsRetainedCurrentEvidenceCannotDetermineWhether":
    "Severity is retained; current evidence cannot determine whether the repository has recovered.",
  "system.latestSample": "Latest sample",
  "system.historicalSample": "Historical sample",
  "system.byteValuesAreApproximateSeverityComesFromThe":
    "Byte values are approximate; severity comes from the server.",
  "system.noValidCapacitySampleYet": "No valid capacity sample yet",
  "system.warningCondition": "Warning condition",
  "system.atOrAbove": "At or above",
  "system.seconds": "seconds",
  "system.criticalCondition": "Critical condition",
  "system.recoveryCondition": "Recovery condition",
  "system.strictlyBelow": "Strictly below",
  "system.maximumSampleAge": "Maximum sample age",
  "system.lastEvaluation": "Last evaluation",
  "system.notEvaluatedYet": "Not evaluated yet",
  "system.warningEvidenceSince": "Warning evidence since",
  "system.criticalEvidenceSince": "Critical evidence since",
  "system.recoveryEvidenceSince": "Recovery evidence since",
  "system.notificationTarget": "Notification target",
  "system.recipientConfigured": "Recipient configured",
  "system.recipientMissingOrTargetUnavailable":
    "Recipient missing or target unavailable",
  "system.targetState": "Target state",
  "system.unavailable": "Unavailable",
  "system.boundTargetVersion": "Bound target version",
  "system.currentTargetVersion": "Current target version",
  "system.theTargetVersionChangedEditAndSaveTo":
    "The target version changed. Edit and save to bind it again; previously unqueued events are not caught up.",
  "system.disableRule": "Disable rule",
  "system.enableRule": "Enable rule",
  "system.deleteRule": "Delete rule",
  "system.onlyPlatformAdministratorsCanManageQuotaAlerts":
    "Only platform administrators can manage quota alerts",
  "system.repositoryQuotaAlerts": "Repository quota alerts",
  "system.observesLogicalRepositoryUsageNotThePhysicalCapacity":
    "Observes logical repository usage, not the physical capacity of S3, NAS, or local mounts.",
  "system.refresh": "Refresh",
  "system.createRule": "Create rule",
  "system.showingPreviouslyReadData": "Showing previously read data",
  "system.readAt": "Read at",
  "system.ruleDeletedHistoryRemainsAvailable":
    "Rule deleted; history remains available",
  "system.ruleSaved": "Rule saved",
  "system.disabledRulesCanBeSavedAnAdministratorMust":
    "Disabled rules can be saved. An administrator must configure the email channel and encryption before enabling them.",
  "system.noEmailTargets": "No email targets",
  "system.createATargetInTheSystemRuntimeEmail":
    "Create a target in the System runtime Email notifications tab, then return here to configure a rule.",
  "system.backToRules": "Back to rules",
  "system.noQuotaAlertRules": "No quota alert rules",
  "system.newRulesAreDisabledByDefaultCreatingOne":
    "New rules are disabled by default. Creating one sends no test email.",
  "system.recoveryBelow": "Recovery below",
  "system.view": "View {name}",
  "system.view2": "View",
  "system.targetSettingsChangedRefreshTheLatestTargetAnd":
    "Target settings changed. Refresh the latest target and confirm again.",
  "system.thisTestKeyBelongsToADifferentRequest":
    "This test key belongs to a different request. Cancel, refresh the target, and confirm again.",
  "system.theTargetIsDisabledCancelAndRefreshNo":
    "The target is disabled. Cancel and refresh; no send was automatically retried.",
  "system.theTargetChangedCancelRefreshAndConfirmAgain":
    "The target changed. Cancel, refresh, and confirm again.",
  "system.theTestRateLimitWasReachedRetryThe":
    "The test rate limit was reached. Retry the same test later.",
  "system.theDeliveryQueueIsFullRetryTheSame":
    "The delivery queue is full. Retry the same test later.",
  "system.encryptionIsUnavailableContactTheDeploymentAdministrator":
    "Encryption is unavailable. Contact the deployment administrator.",
  "system.theEmailChannelIsDisabledContactTheDeployment":
    "The email channel is disabled. Contact the deployment administrator.",
  "system.validationFailedCheckTheNameSingleRecipientAddress":
    "Validation failed. Check the name, single recipient address, and language.",
  "system.theTargetOrEndpointIsUnavailableCancelAnd":
    "The target or endpoint is unavailable. Cancel and refresh.",
  "system.emailAccessDeniedConfirmPlatformAdministratorAccessAnd":
    "Email access denied. Confirm platform administrator access and refresh.",
  "system.thisSessionCannotAccessEmailSettingsSignIn":
    "This session cannot access email settings. Sign in again.",
  "system.theSessionExpiredSignInAgain": "The session expired. Sign in again.",
  "system.couldNotReadEmailNotificationDataRetryThe":
    "Could not read email notification data. Retry the read.",
  "system.couldNotConfirmTheSaveRefreshAndCheck":
    "Could not confirm the save. Refresh and check target settings before retrying.",
  "system.couldNotGenerateTheTemplatePreviewRetryThe":
    "Could not generate the template preview. Retry the preview; this operation sends no mail.",
  "system.theRequestFailedAndItsResultMayBe":
    "The request failed and its result may be unknown. Test retries use the same key; refresh to check delivery results.",
  "system.theReadRequestWasInvalidRefreshAndCheck":
    "The read request was invalid. Refresh and check the Gateway version.",
  "system.targetValidationFailedCheckTheNameSingleRecipient":
    "Target validation failed. Check the name, single recipient address, and language.",
  "system.previewValidationFailedCheckTheScenarioAndLanguage":
    "Preview validation failed. Check the scenario and language.",
  "system.testValidationFailedCheckTheTargetScenarioAnd":
    "Test validation failed. Check the target, scenario, and language.",
  "system.targetSettingsChangedCancelThisTestRefreshThe":
    "Target settings changed. Cancel this test, refresh the target, and confirm again.",
  "system.readsAreRateLimitedRetryTheReadLater":
    "Reads are rate limited. Retry the read later.",
  "system.emailSettingsChangedRetryTheRead":
    "Email settings changed. Retry the read.",
  "system.emailResourcesOrEndpointsAreUnavailableRetryThe":
    "Email resources or endpoints are unavailable. Retry the read and check the Gateway version.",
  "system.saveRequestsAreRateLimitedRetryLater":
    "Save requests are rate limited. Retry later.",
  "system.theEmailTargetWasDisabledRefreshAndCheck":
    "The email target was disabled. Refresh and check target settings.",
  "system.theEmailTargetChangedRefreshAndConfirmIts":
    "The email target changed. Refresh and confirm its settings again.",
  "system.previewRequestsAreRateLimitedRetryThePreview":
    "Preview requests are rate limited. Retry the preview later.",
  "system.theTemplatePreviewEndpointIsUnavailableRefreshAnd":
    "The template preview endpoint is unavailable. Refresh and check the Gateway version.",
  "system.enterATargetName": "Enter a target name.",
  "system.useA1128CharacterNameWithoutLine":
    "Use a 1–128 character name without line breaks or null characters.",
  "system.enterOneRecipientEmailAddress": "Enter one recipient email address.",
  "system.enterOneValidMailboxWithoutADisplayName":
    "Enter one valid mailbox without a display name, separators, or line breaks.",
  "system.editEmailTarget": "Edit email target",
  "system.createDisabledEmailTarget": "Create disabled email target",
  "system.targetName": "Target name",
  "system.useATeamOrPurposeNameDoNot":
    "Use a team or purpose name; do not put a mailbox in the name.",
  "system.emailLanguage": "Email language",
  "system.recipientAddress": "Recipient address",
  "system.theExistingAddressIsWriteOnlyLeaveEmpty":
    "The existing address is write-only. Leave empty to retain it or enter a replacement; cancel or conflict clears the input.",
  "system.oneRecipientOnlyItsAddressIsNeverReturned":
    "One recipient only; its address is never returned. New targets are disabled.",
  "system.savingDoesNotEnableTheTargetOrSend":
    "Saving does not enable the target or send mail.",
  "system.refreshLatestTargetAndEditAgain":
    "Refresh latest target and edit again",
  "system.saveTarget": "Save target",
  "system.saveDisabledTarget": "Save disabled target",
  "system.confirmSyntheticTestSend": "Confirm synthetic test send",
  "system.confirmEnablingEmailTarget": "Confirm enabling email target",
  "system.confirmDisablingEmailTarget": "Confirm disabling email target",
  "system.target": "Target",
  "system.identityAndConfirmedVersion": "Identity and confirmed version",
  "system.emailLanguageAndRecipient": "Email language and recipient",
  "system.oneConfiguredRecipientTheAddressCannotBeRead":
    "One configured recipient; the address cannot be read",
  "system.syntheticScenario": "Synthetic scenario",
  "system.confirmationSendsOneEmailToThisTargetS":
    "Confirmation sends one email to this target's configured recipient",
  "system.theDataIsSyntheticTheRequestEntersThe":
    "The data is synthetic. The request enters the real delivery queue and is rate limited. Cancelling cannot recall queued or in-flight mail. SMTP acceptance does not mean inbox delivery.",
  "system.retryKeepsThisTargetVersionScenarioAndTest":
    "Retry keeps this target, version, scenario, and test key; it does not automatically create another test.",
  "system.enablingSendsNoTestEmail": "Enabling sends no test email",
  "system.disablingCannotRecallInFlightMail":
    "Disabling cannot recall in-flight mail",
  "system.theTargetVersionChangesExistingQuotaRulesBind":
    "The target version changes. Existing quota rules bind the old version and require route confirmation on the rules page; queued old-version mail will stop.",
  "system.confirmSyntheticTestSend2": "Confirm synthetic test send",
  "system.emailTemplatePreview": "Email template preview",
  "system.offlineSyntheticSampleNoRecipientReadQueueEntry":
    "Offline synthetic sample: no recipient read, queue entry, or send. Links and external resources are disabled in the sandbox preview.",
  "system.previewScenario": "Preview scenario",
  "system.previewLanguage": "Preview language",
  "system.previewTemplate": "Preview template",
  "system.previewFormat": "Preview format",
  "system.plainText": "Plain text",
  "system.templateVersion": "Template version",
  "system.syntheticEmailHTMLPreview": "Synthetic email HTML preview",
  "system.chooseAScenarioAndPreviewTheTemplate":
    "Choose a scenario and preview the template",
  "system.warningCriticalAndRecoveryProvideEnglishAndChinese":
    "Warning, critical, and recovery provide English and Chinese HTML and equivalent plain text.",
  "system.recentDeliveryResultsUpTo50": "Recent delivery results (up to 50)",
  "system.sMTPAcceptanceDoesNotMeanInboxDeliveryOr":
    "SMTP acceptance does not mean inbox delivery or reading. Disabling cannot recall in-flight mail; uncertain retries may duplicate mail.",
  "system.noEmailDeliveriesYet": "No email deliveries yet",
  "system.repositoryQuotaAlert": "Repository quota alert",
  "system.administratorSyntheticTest": "Administrator synthetic test",
  "system.deliveryID": "Delivery ID",
  "system.updated": "Updated",
  "system.nextAttempt2": "Next attempt",
  "system.accepted": "Accepted",
  "system.possibleDuplicateAnEarlierSMTPAttemptHadAn":
    "Possible duplicate: an earlier SMTP attempt had an uncertain outcome.",
  "system.onlyPlatformAdministratorsCanManageEmailNotifications":
    "Only platform administrators can manage email notifications",
  "system.emailNotifications": "Email notifications",
  "system.manageWriteOnlyRecipientTargetsPreviewTemplatesAnd":
    "Manage write-only recipient targets, preview templates, and explicitly send synthetic tests.",
  "system.syntheticTestQueuedCheckDeliveryResults":
    "Synthetic test queued; check delivery results",
  "system.emailTargetSavedNoTestSent": "Email target saved; no test sent",
  "system.aDeploymentAdministratorConfiguresTheChannelThisPage":
    "A deployment administrator configures the channel; this page accepts no SMTP credentials. Preview remains available; saving targets requires usable encryption.",
  "system.emailTargets": "Email targets",
  "system.newEmailTarget": "New email target",
  "system.newTargetsAreDisabledSavingSendsNoMail":
    "New targets are disabled; saving sends no mail.",
  "system.recipientConfiguredWriteOnly": "Recipient configured (write-only)",
  "system.recipientNotConfigured": "Recipient not configured",
  "system.edit": "Edit {name}",
  "system.edit2": "Edit",
  "system.disable": "Disable",
  "system.enable": "Enable",
  "system.test": "Test {name}",
  "system.sendSyntheticTest": "Send synthetic test…",
  "system.refreshesEvery30SecondsWhileVisible":
    "Refreshes every 30 seconds while visible",
  "system.systemRuntime": "System runtime",
  "system.inspectGatewayNodesDiagnosticsLogsRepositoryQuotaAlerts":
    "Inspect Gateway nodes, diagnostics, logs, repository quota alerts, and email notifications.",
  "system.systemDiagnostics": "System diagnostics",
  "system.runtimeLogs": "Runtime logs",
  "system.quotaAlerts": "Quota alerts",
  "system.warningThreshold": "Warning threshold (%)",
  "system.warningHoldDuration": "Warning hold duration (seconds)",
  "system.criticalThreshold": "Critical threshold (%)",
  "system.criticalHoldDuration": "Critical hold duration (seconds)",
  "system.recoveryThreshold": "Recovery threshold (%)",
  "system.recoveryHoldDuration": "Recovery hold duration (seconds)",
  "system.disableTarget": "Disable {name}",
  "system.enableTarget": "Enable {name}",
  "system.theTargetIsEnabledSavingRetainsThisState":
    "The target is enabled; saving retains this state and sends no test.",
  "system.theTargetIsDisabledSavingRetainsThisState":
    "The target is disabled; saving retains this state and sends no test.",
};
