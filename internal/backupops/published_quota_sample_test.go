package backupops

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/emailnotification"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// A published recovery source has its original schema. Its own verified binary
// must sample quota; the candidate's all-format capacity projection belongs only
// to the candidate schema. This scheduler cannot send SMTP and is removed before
// the fixture records the offline backup boundary.
func samplePublishedRecoveryQuota(t *testing.T, ctx context.Context, source *OwnedTarget, store *repository.PostgresStore, endpoint, ruleID string, mail repository.QuotaAlertMailConfig) {
	t.Helper()
	release := source.spec.Release
	if release.NativeArchive == nil || release.Identity.Version != "0.6.0" || release.Identity.Artifact == nil || release.Identity.Artifact.Kind != "binary" || source.checkOwned(ctx) != nil {
		t.Fatal("published quota fixture requires its verified original release")
	}
	binary := filepath.Join(source.directory, "gateway")
	if sum, _, err := describeBinary(binary); err != nil || sum != release.Identity.Artifact.SHA256 {
		t.Fatal("published quota runtime identity changed")
	}
	before, err := store.GetRepositoryQuotaAlertRule(ctx, ruleID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := fixtureHTTP(t, ctx, endpoint, "GET", "/api/v2/repositories/"+before.RepositoryID+"/capacity", source.spec.AdminToken, nil, 200, "")
	var capacity struct {
		RepositoryID, Format               string
		UsedBytes, QuotaBytes, ObjectCount int64
	}
	if json.Unmarshal(data, &capacity) != nil || capacity.RepositoryID != before.RepositoryID || capacity.Format != "raw" || capacity.UsedBytes <= 0 || capacity.QuotaBytes <= 0 || capacity.ObjectCount != 1 {
		t.Fatal("published capacity identity or usage missing")
	}
	config := emailnotification.Config{Enabled: mail.Enabled, Host: "smtp.example.test", Port: 465, Mode: "implicit_tls", From: mail.From, ConsoleOrigin: mail.ConsoleOrigin, ApprovedIPs: []string{"192.0.2.10"}}
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "quota-mail.json")
	if err = os.WriteFile(configPath, body, 0644); err != nil {
		t.Fatal(err)
	}
	image, err := binaryRuntimeImage(release.Identity.Artifact.Platform)
	if err != nil {
		t.Fatal(err)
	}
	name := source.spec.Project + "-quota-" + uuid.NewString()[:8]
	invocation := uuid.NewString()
	docker := source.spec.Docker
	id := ""
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		data, err := docker.output(cleanupCtx, "container", "inspect", name)
		var containers []struct {
			ID, Name   string
			Config     struct{ Labels map[string]string }
			HostConfig struct{ NetworkMode string }
		}
		if err != nil || json.Unmarshal(data, &containers) != nil || len(containers) != 1 || strings.TrimPrefix(containers[0].Name, "/") != name || (id != "" && containers[0].ID != id) || containers[0].Config.Labels["artifact-gateway.restore-owner"] != source.owner || containers[0].Config.Labels["artifact-gateway.quota-sample"] != invocation || containers[0].HostConfig.NetworkMode != source.spec.Project+"-network" {
			t.Error("published quota sampler cleanup ownership unavailable")
			return
		}
		if _, err = docker.output(cleanupCtx, "rm", "-f", containers[0].ID); err != nil {
			t.Error("published quota sampler cleanup failed")
		}
	}()
	environment := []string{
		"GATEWAY_DATABASE_URL=postgres://gateway:" + escapePassword(source.spec.PostgresPassword) + "@postgres:5432/gateway?sslmode=disable",
		"GATEWAY_RUSTFS_ENDPOINT=http://rustfs:9000", "GATEWAY_RUSTFS_BUCKET=gateway-cache",
		"GATEWAY_RUSTFS_ACCESS_KEY=" + source.spec.AccessKey, "GATEWAY_RUSTFS_SECRET_KEY=" + source.spec.SecretKey,
		"GATEWAY_ADMIN_TOKEN=" + source.spec.AdminToken, "GATEWAY_RESOLVER_TOKEN=" + source.spec.ResolverToken,
		"GATEWAY_NODE_ROLES=scheduler", "GATEWAY_LISTEN_ADDRESS=:0", "GATEWAY_INSTANCE_ID=" + name,
		"GATEWAY_SETTINGS_ENCRYPTION_KEY=" + recoveryEmailKey, "GATEWAY_EMAIL_CONFIG_FILE=/quota-mail.json",
	}
	args := []string{"create", "--name", name, "--network", source.spec.Project + "-network", "--label", "artifact-gateway.restore-owner=" + source.owner, "--label", "artifact-gateway.quota-sample=" + invocation, "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=256m,mode=1777", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--mount", "type=bind,source=" + binary + ",target=/gateway,readonly", "--mount", "type=bind,source=" + configPath + ",target=/quota-mail.json,readonly", "--entrypoint", "/gateway", "--platform", release.Identity.Artifact.Platform}
	for _, value := range environment {
		key, _, _ := strings.Cut(value, "=")
		args = append(args, "--env", key)
	}
	args = append(args, image)
	out, err := docker.command(ctx, environment, args...).Output()
	if receipt := strings.TrimSpace(string(out)); regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(receipt) {
		id = receipt
	}
	if err != nil || id == "" {
		t.Fatal("published quota sampler creation failed")
	}
	if _, err = docker.output(ctx, "start", id); err != nil {
		t.Fatal("published quota sampler startup failed")
	}
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		after, err := store.GetRepositoryQuotaAlertRule(ctx, ruleID)
		if err != nil {
			t.Fatal(err)
		}
		if after.StateVersion != before.StateVersion {
			if !after.EvaluatedAt.After(before.EvaluatedAt) || after.State.DataState != "available" || after.State.UsedBytes != capacity.UsedBytes || after.State.QuotaBytes != capacity.QuotaBytes {
				t.Fatal("published quota sample differs from original runtime capacity")
			}
			t.Logf("original published %s quota sample committed: stateVersion %s -> %s, evaluatedAt %s -> %s, usedBytes=%d quotaBytes=%d", release.Identity.Version, before.StateVersion, after.StateVersion, before.EvaluatedAt.Format(time.RFC3339Nano), after.EvaluatedAt.Format(time.RFC3339Nano), after.State.UsedBytes, after.State.QuotaBytes)
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("published quota sampler cancelled")
		case <-deadline.C:
			t.Fatal("published quota sampler did not commit a real sample")
		case <-ticker.C:
		}
	}
}
