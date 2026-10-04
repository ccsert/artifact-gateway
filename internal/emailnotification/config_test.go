package emailnotification

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRelayConfigDefaultsOffAndKeepsErrorsSafe(t *testing.T) {
	if c, err := Load(""); err != nil || c.Enabled {
		t.Fatal("default enabled")
	}
	path := filepath.Join(t.TempDir(), "relay.json")
	for _, content := range []string{`{"enabled":false}`, `{"enabled":true,"host":"private-host.example.test","port":465,"mode":"plaintext","from":"gateway@example.test","approvedIPs":["192.0.2.10"]}`, `{"enabled":true,"host":"smtp.example.test","port":465,"mode":"implicit_tls","from":"gateway@example.test","approvedIPs":["169.254.169.254"]}`, `{"enabled":true,"unrecognized":"secret"}`} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		c, err := Load(path)
		if content == `{"enabled":false}` {
			if err != nil || c.Enabled {
				t.Fatal("disabled config rejected")
			}
		} else if err != ErrInvalidConfig {
			t.Fatal("unsafe relay config or unsafe error")
		}
	}
	if err := os.WriteFile(path, []byte(`{"enabled":true,"host":"smtp.example.test","port":465,"mode":"implicit_tls","from":"gateway@example.test","approvedIPs":["192.0.2.10"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(path); err != nil || !c.Enabled {
		t.Fatal("valid config rejected")
	}
	auth := filepath.Join(t.TempDir(), "synthetic-auth.json")
	if err := os.WriteFile(auth, []byte(`{"username":"synthetic","password":"fixture-only"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readAuth(auth); err == nil {
		t.Fatal("world-readable auth accepted")
	}
	if err := os.Chmod(auth, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAuth(auth); err != nil {
		t.Fatal("private synthetic auth rejected")
	}
}

func TestRelayRejectsIPv6MetadataWithoutRejectingPrivateSMTP(t *testing.T) {
	c := Config{Enabled: true, Host: "smtp.example.test", Port: 465, Mode: "implicit_tls", From: "gateway@example.test", ApprovedIPs: []string{"fd00:ec2::254"}}
	if c.Validate() == nil {
		t.Fatal("AWS IPv6 metadata accepted")
	}
	c.ApprovedIPs = []string{"fd00:1234::25"}
	if c.Validate() != nil {
		t.Fatal("explicit private SMTP denied")
	}
}
