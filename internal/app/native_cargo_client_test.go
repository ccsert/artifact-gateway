package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"net/http/httptest"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestNativeCargoOfficialClientHostedFlow(t *testing.T) {
	cargoPath, err := exec.LookPath("cargo")
	if err != nil {
		if os.Getenv("CARGO_REQUIRED") == "1" {
			t.Fatal("Cargo 1.96.0 is required for the official Hosted contract")
		}
		t.Skip("Cargo is unavailable")
	}
	versionOutput, err := exec.Command(cargoPath, "--version").Output()
	if err != nil || !strings.HasPrefix(string(versionOutput), "cargo 1.96.0 ") {
		if os.Getenv("CARGO_REQUIRED") == "1" {
			t.Fatalf("Cargo 1.96.0 is required, got %q: %v", versionOutput, err)
		}
		t.Skipf("Cargo 1.96.0 is unavailable: %q", versionOutput)
	}
	store := repository.NewMemoryStore()
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "cargo-official", Name: "cargo-official", Format: repository.FormatCargo,
	}); err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	server := httptest.NewServer(NewGatewayHandler(Dependencies{NativeCargoObjectStore: objects},
		store, TestAdapter{}, testAuthenticator()))
	defer server.Close()
	home := t.TempDir()
	config := "[registries.fixture]\nindex = \"sparse+" + server.URL + "/cargo/cargo-official/\"\ncredential-provider = \"cargo:token\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	packageDir := filepath.Join(t.TempDir(), "package")
	consumerDir := filepath.Join(t.TempDir(), "consumer")
	for _, dir := range []string{filepath.Join(packageDir, "src"), filepath.Join(consumerDir, "src")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(packageDir, "Cargo.toml"), "[package]\nname = \"gateway-official-cargo\"\nversion = \"0.2.0\"\nedition = \"2024\"\nlicense = \"MIT\"\ndescription = \"Gateway official Cargo client fixture\"\n")
	write(filepath.Join(packageDir, "src", "lib.rs"), "pub fn answer() -> u8 { 42 }\n")
	write(filepath.Join(packageDir, "src", "main.rs"), "fn main() { println!(\"{}\", gateway_official_cargo::answer()); }\n")
	write(filepath.Join(consumerDir, "Cargo.toml"), "[package]\nname = \"cargo-gateway-consumer\"\nversion = \"0.1.0\"\nedition = \"2024\"\n")
	write(filepath.Join(consumerDir, "src", "main.rs"), "fn main() { println!(\"{}\", gateway_official_cargo::answer()); }\n")
	environment := append(os.Environ(), "CARGO_HOME="+home, "CARGO_TARGET_DIR="+filepath.Join(t.TempDir(), "target"),
		"CARGO_REGISTRIES_FIXTURE_TOKEN=admin-secret", "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=127.0.0.1,localhost")
	runWithEnvironment := func(env []string, dir string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, cargoPath, args...)
		command.Dir = dir
		command.Env = env
		if output, runErr := command.CombinedOutput(); runErr != nil {
			t.Fatalf("cargo %s: %v\n%s", strings.Join(args, " "), runErr, output)
		}
	}
	run := func(dir string, args ...string) { runWithEnvironment(environment, dir, args...) }
	run(packageDir, "publish", "--registry", "fixture", "--allow-dirty", "--no-verify")
	run(consumerDir, "add", "gateway-official-cargo", "--registry", "fixture")
	run(consumerDir, "check")
	run(consumerDir, "install", "gateway-official-cargo", "--registry", "fixture", "--root", filepath.Join(t.TempDir(), "install"))
	run(packageDir, "search", "gateway-official-cargo", "--registry", "fixture")
	run(packageDir, "yank", "gateway-official-cargo@0.2.0", "--registry", "fixture")
	run(consumerDir, "check", "--locked")
	yankedHome := t.TempDir()
	write(filepath.Join(yankedHome, "config.toml"), config)
	yankedEnvironment := append(os.Environ(), "CARGO_HOME="+yankedHome, "CARGO_TARGET_DIR="+filepath.Join(t.TempDir(), "yanked-target"),
		"CARGO_REGISTRIES_FIXTURE_TOKEN=admin-secret", "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=127.0.0.1,localhost")
	for _, fixture := range []struct {
		name     string
		lockfile bool
	}{{"fresh", false}, {"locked", true}} {
		directory := filepath.Join(t.TempDir(), fixture.name)
		if err := os.MkdirAll(filepath.Join(directory, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, file := range []string{"Cargo.toml", "src/main.rs"} {
			content, err := os.ReadFile(filepath.Join(consumerDir, file))
			if err != nil {
				t.Fatal(err)
			}
			write(filepath.Join(directory, file), string(content))
		}
		if fixture.lockfile {
			content, err := os.ReadFile(filepath.Join(consumerDir, "Cargo.lock"))
			if err != nil {
				t.Fatal(err)
			}
			write(filepath.Join(directory, "Cargo.lock"), string(content))
			runWithEnvironment(yankedEnvironment, directory, "check", "--locked")
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		command := exec.CommandContext(ctx, cargoPath, "check")
		command.Dir = directory
		command.Env = yankedEnvironment
		output, err := command.CombinedOutput()
		cancel()
		if err == nil || !strings.Contains(string(output), "failed to select a version") {
			t.Fatalf("new resolution selected yanked version: err=%v output=%s", err, output)
		}
	}
	run(packageDir, "yank", "gateway-official-cargo@0.2.0", "--undo", "--registry", "fixture")

	repo, err := store.GetHostedRepositoryByName(context.Background(), "cargo-official")
	if err != nil {
		t.Fatal(err)
	}
	repo.AnonymousRead = true
	if _, err := store.UpdateHostedRepository(context.Background(), repo, repo.Version); err != nil {
		t.Fatal(err)
	}
	enableAnonymousAccess(t, store)
	publicHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(publicHome, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	publicConsumer := filepath.Join(t.TempDir(), "public-consumer")
	if err := os.MkdirAll(filepath.Join(publicConsumer, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(publicConsumer, "Cargo.toml"), "[package]\nname = \"cargo-gateway-public-consumer\"\nversion = \"0.1.0\"\nedition = \"2024\"\n")
	write(filepath.Join(publicConsumer, "src", "main.rs"), "fn main() { println!(\"{}\", gateway_official_cargo::answer()); }\n")
	publicEnvironment := append(os.Environ(), "CARGO_HOME="+publicHome, "CARGO_TARGET_DIR="+filepath.Join(t.TempDir(), "public-target"),
		"CARGO_REGISTRIES_FIXTURE_TOKEN=", "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=127.0.0.1,localhost")
	runWithEnvironment(publicEnvironment, publicConsumer, "search", "gateway-official-cargo", "--registry", "fixture")
	runWithEnvironment(publicEnvironment, publicConsumer, "add", "gateway-official-cargo", "--registry", "fixture")
	runWithEnvironment(publicEnvironment, publicConsumer, "check")
	runWithEnvironment(publicEnvironment, publicConsumer, "install", "gateway-official-cargo", "--registry", "fixture", "--root", filepath.Join(t.TempDir(), "public-install"))
	if _, err := store.TombstoneCargoPublication(context.Background(), repo.ID, "gateway-official-cargo", "0.2.0"); err != nil {
		t.Fatal(err)
	}
	blockedHome := t.TempDir()
	write(filepath.Join(blockedHome, "config.toml"), config)
	blockedEnvironment := append(os.Environ(), "CARGO_HOME="+blockedHome, "CARGO_TARGET_DIR="+filepath.Join(t.TempDir(), "blocked-target"),
		"CARGO_REGISTRIES_FIXTURE_TOKEN=", "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=127.0.0.1,localhost")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	blocked := exec.CommandContext(ctx, cargoPath, "install", "gateway-official-cargo", "--registry", "fixture", "--root", filepath.Join(t.TempDir(), "blocked-install"))
	blocked.Dir, blocked.Env = publicConsumer, blockedEnvironment
	blockedOutput, blockedErr := blocked.CombinedOutput()
	cancel()
	if blockedErr == nil {
		t.Fatalf("official Cargo installed a tombstoned crate: %s", blockedOutput)
	}
	if _, err := store.RestoreCargoPublication(context.Background(), repo.ID, "gateway-official-cargo", "0.2.0"); err != nil {
		t.Fatal(err)
	}
	restoredHome := t.TempDir()
	write(filepath.Join(restoredHome, "config.toml"), config)
	restoredEnvironment := append(os.Environ(), "CARGO_HOME="+restoredHome, "CARGO_TARGET_DIR="+filepath.Join(t.TempDir(), "restored-target"),
		"CARGO_REGISTRIES_FIXTURE_TOKEN=", "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=127.0.0.1,localhost")
	runWithEnvironment(restoredEnvironment, publicConsumer, "install", "gateway-official-cargo", "--registry", "fixture", "--root", filepath.Join(t.TempDir(), "restored-install"))
	publication, err := store.GetCargoPublication(context.Background(), repo.ID, "gateway-official-cargo", "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	promotionTarget, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "cargo-official-promoted", Format: repository.FormatCargo,
	})
	if err != nil {
		t.Fatal(err)
	}
	replicationTarget, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: uuid.NewString(), Name: "cargo-official-replicated", Format: repository.FormatCargo,
	})
	if err != nil {
		t.Fatal(err)
	}
	promoter := NativeCargoPromotion{Store: store, Objects: objects}
	if _, _, err := promoter.Enqueue(context.Background(), promotionTarget.ID, "official-promote", CargoPromotionPayload{
		SourceRepositoryID: repo.ID, Name: publication.Name, Version: publication.Version, Digest: publication.Digest,
	}); err != nil {
		t.Fatal(err)
	}
	if err := promoter.RunJobs(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	plan, _, err := store.CreateReplicationPlan(context.Background(), repository.ReplicationPlan{
		ID: uuid.NewString(), SourceRepositoryID: repo.ID, TargetRepositoryID: replicationTarget.ID,
		Format: repository.FormatCargo, Coordinate: publication.Name + "@" + publication.Version,
		Digest: publication.Digest, IdempotencyKey: "official-replicate",
	}, []repository.ReplicationCheckpoint{{SourceObjectKey: publication.ObjectKey,
		ObjectKey: publication.ObjectKey, Digest: publication.Digest, Size: publication.Size}})
	if err != nil {
		t.Fatal(err)
	}
	if err := (CargoReplication{Store: store, Source: objects, Destination: objects}).RunJobs(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if completed, err := store.GetReplicationPlan(context.Background(), replicationTarget.ID, plan.ID); err != nil || completed.State != "completed" {
		t.Fatalf("official replication=%+v err=%v", completed, err)
	}
	for _, target := range []struct {
		registry string
		repo     repository.HostedRepository
	}{{"promoted", promotionTarget}, {"replicated", replicationTarget}} {
		targetHome := t.TempDir()
		write(filepath.Join(targetHome, "config.toml"), "[registries."+target.registry+"]\nindex = \"sparse+"+server.URL+"/cargo/"+target.repo.Name+"/\"\ncredential-provider = \"cargo:token\"\n")
		targetEnvironment := append(os.Environ(), "CARGO_HOME="+targetHome, "CARGO_TARGET_DIR="+filepath.Join(t.TempDir(), "distribution-target"),
			"CARGO_REGISTRIES_"+strings.ToUpper(target.registry)+"_TOKEN=admin-secret", "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=127.0.0.1,localhost")
		runWithEnvironment(targetEnvironment, publicConsumer, "install", "gateway-official-cargo", "--registry", target.registry,
			"--root", filepath.Join(t.TempDir(), "distribution-install"))
	}
}
