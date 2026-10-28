package release_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	fixtures "github.com/defos-ai/commander/.github/tests/fixtures"
)

func TestCIComposeCredentialInitialization(t *testing.T) {
	for _, kind := range fixtures.ComposeCredentialKinds() {
		t.Run(kind.Name, func(t *testing.T) {
			f := fixtures.NewComposeCredential(t, kind)
			service := kind.Name + "-credentials"
			if err := f.Run(t, service, ""); err != nil {
				t.Fatalf("credential generation failed: %v", err)
			}
			path := kind.Name + "-credentials/" + kind.File
			credential := f.Read(t, path)
			if len(strings.TrimSpace(credential)) < 32 {
				t.Fatal("generated credential is empty or too short")
			}
			for path, mode := range map[string]os.FileMode{kind.Name + "-credentials": 0750, path: 0440} {
				info, err := os.Stat(filepath.Join(f.Dir(), path))
				if err != nil || info.Mode().Perm() != mode {
					t.Fatalf("%s must be readable by the consumer group and inaccessible to others", path)
				}
			}
			if err := f.Run(t, service, ""); err != nil {
				t.Fatalf("credential reuse failed: %v", err)
			}
			if f.Read(t, path) != credential {
				t.Fatal("restart replaced the saved credential")
			}
			for _, override := range []string{"configured-credential-first", "configured-credential-second"} {
				if err := f.Run(t, service, override); err != nil {
					t.Fatalf("explicit credential update failed: %v", err)
				}
				if strings.TrimSpace(f.Read(t, path)) != override {
					t.Fatal("explicit credential was not saved for both consumers")
				}
			}
			before := f.Read(t, path)
			if err := f.Run(t, service, "invalid\ncredential"); err == nil {
				t.Fatal("credential validation did not reject an invalid override")
			}
			if f.Read(t, path) != before {
				t.Fatal("invalid override replaced the saved credential")
			}
			entries, err := os.ReadDir(f.State())
			if err != nil || len(entries) != 1 || entries[0].Name() != kind.File {
				t.Fatal("credential initialization left temporary files")
			}
		})
	}
}

func TestCIComposeCredentialInitializationRejectsInvalidState(t *testing.T) {
	for _, credentialKind := range fixtures.ComposeCredentialKinds() {
		for _, state := range []string{"empty", "directory", "symlink", "dangling symlink", "invalid API password"} {
			if state == "invalid API password" && credentialKind.Name != "api" {
				continue
			}
			t.Run(credentialKind.Name+"/"+state, func(t *testing.T) {
				f := fixtures.NewComposeCredential(t, credentialKind)
				path := filepath.Join(f.State(), credentialKind.File)
				var err error
				switch state {
				case "empty":
					err = os.WriteFile(path, nil, 0600)
				case "invalid API password":
					err = os.WriteFile(path, []byte("   \t"), 0600)
				case "directory":
					err = os.Mkdir(path, 0700)
				case "symlink":
					if err = os.WriteFile(filepath.Join(f.Dir(), "outside"), []byte("existing test credential"), 0600); err == nil {
						err = os.Symlink(filepath.Join(f.Dir(), "outside"), path)
					}
				case "dangling symlink":
					err = os.Symlink(filepath.Join(f.Dir(), "absent"), path)
				}
				if err != nil {
					t.Fatal(err)
				}
				before, err := os.Lstat(path)
				if err != nil {
					t.Fatal(err)
				}
				data, _ := os.ReadFile(path)
				for _, override := range []string{"", "override-must-not-replace-invalid-state"} {
					if err := f.Run(t, credentialKind.Name+"-credentials", override); err == nil {
						t.Fatal("invalid credential state was accepted")
					}
					after, err := os.Lstat(path)
					if err != nil || !os.SameFile(before, after) {
						t.Fatal("initialization replaced invalid persisted state")
					}
					current, _ := os.ReadFile(path)
					if string(current) != string(data) {
						t.Fatal("initialization changed invalid persisted bytes")
					}
				}
			})
		}
	}
}

func TestCIComposeCredentialConsumers(t *testing.T) {
	for _, kind := range fixtures.ComposeCredentialKinds() {
		consumers := []string{kind.Name, "playground"}
		if kind.Name == "api" {
			consumers = append(consumers, "missions")
		}
		for _, service := range consumers {
			t.Run(kind.Name+"/"+service, func(t *testing.T) {
				for _, override := range []string{"", "explicit-consumer-credential"} {
					f := fixtures.NewComposeCredential(t, kind)
					if err := f.Run(t, kind.Name+"-credentials", "shared-test-credential"); err != nil {
						t.Fatalf("credential init failed: %v", err)
					}
					arguments := []string{"--option=value with spaces", "", "--another"}
					if err := f.Run(t, service, override, arguments...); err != nil {
						t.Fatalf("consumer startup failed: %v", err)
					}
					want := "shared-test-credential"
					if override != "" && service != "api" && service != "missions" {
						want = override
					}
					if f.Read(t, "observed-"+kind.Name) != want {
						t.Fatal("consumer did not receive the selected credential")
					}
					if len(f.Service(service).Entrypoint) == 4 && f.Read(t, "observed-arguments") != strings.Join(arguments, "\n")+"\n" {
						t.Fatal("entrypoint changed the application arguments")
					}
					if f.Read(t, "before-pid") != f.Read(t, "after-pid") {
						t.Fatal("entrypoint did not replace the shell process")
					}
				}
				f := fixtures.NewComposeCredential(t, kind)
				if err := f.Run(t, service, ""); err == nil {
					t.Fatal("consumer started without its credential file")
				}
				if _, err := os.Stat(filepath.Join(f.Dir(), "observed-"+kind.Name)); !os.IsNotExist(err) {
					t.Fatal("application launched before reading the credential")
				}
				err := f.Run(t, service, "explicit-without-file")
				if service == "api" || service == "missions" {
					if err == nil {
						t.Fatal("local API consumers must always use the initializer's persisted password")
					}
				} else if err != nil || f.Read(t, "observed-"+kind.Name) != "explicit-without-file" {
					t.Fatal("consumer must retain explicit credentials without requiring a saved file")
				}
			})
		}
	}
	// Environment and argument overrides must never inherit the local stack's password.
	for _, target := range []struct {
		name, url string
		arguments []string
	}{
		{name: "other host", url: "http://other-api:4274"},
		{name: "trailing slash", url: "http://api:4274/"},
		{name: "other scheme", url: "https://api:4274"},
		{name: "long flag", url: "http://api:4274", arguments: []string{"--api-url", "http://other-api:4274"}},
		{name: "short flag", url: "http://api:4274", arguments: []string{"-api-url", "http://other-api:4274"}},
		{name: "long equals flag", url: "http://api:4274", arguments: []string{"--api-url=http://other-api:4274"}},
		{name: "short equals flag", url: "http://api:4274", arguments: []string{"-api-url=http://other-api:4274"}},
		{name: "explicit local flag", url: "http://api:4274", arguments: []string{"--api-url=http://api:4274"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			f := fixtures.NewComposeCredential(t, fixtures.ComposeCredentialKinds()[1])
			if err := f.Run(t, "api-credentials", "shared-test-credential"); err != nil {
				t.Fatalf("credential init failed: %v", err)
			}
			for _, override := range []string{"", "custom-api-password"} {
				if err := f.RunWithEnvironment(t, "playground", override, map[string]string{"COMMANDER_API_URL": target.url}, target.arguments...); err != nil {
					t.Fatalf("custom API startup failed: %v", err)
				}
				if f.Read(t, "observed-api") != override {
					t.Fatal("custom API received the Compose password or lost its explicit password")
				}
				if f.Read(t, "observed-arguments") != strings.Join(target.arguments, "\n")+"\n" {
					t.Fatal("credential guard changed the API arguments")
				}
			}
		})
	}
}

func TestCIComposeCredentialWiring(t *testing.T) {
	services := fixtures.ComposeServices(t)
	for _, kind := range fixtures.ComposeCredentialKinds() {
		setup := services[kind.Name+"-credentials"]
		if setup.Image != services[kind.Name].Image || setup.User != "0:1000" || !setup.ReadOnly ||
			!slices.Contains(setup.CapDrop, "ALL") || !slices.Contains(setup.SecurityOpt, "no-new-privileges:true") {
			t.Fatalf("%s credential setup must reuse its adapter image with restricted root/group access", kind.Name)
		}
		volume := kind.Name + "-credentials:" + kind.Mount
		if !slices.Equal(setup.Volumes, []string{volume}) {
			t.Fatalf("%s credential setup must own its writable credential volume", kind.Name)
		}
		consumers := []string{kind.Name, "playground"}
		if kind.Name == "api" {
			consumers = append(consumers, "missions")
		}
		for _, name := range consumers {
			service := services[name]
			if !slices.Contains(service.Volumes, volume+":ro") ||
				service.DependsOn[kind.Name+"-credentials"].Condition != "service_completed_successfully" ||
				!slices.Contains(service.CapDrop, "ALL") || !slices.Contains(service.SecurityOpt, "no-new-privileges:true") {
				t.Fatalf("%s must wait for %s setup and mount credentials read-only without extra privileges", name, kind.Name)
			}
		}
	}
	apiSetup := services["api-credentials"]
	if apiSetup.NetworkMode != "none" || apiSetup.Environment["API_USERNAME"] != "${API_USERNAME:-commander}" ||
		apiSetup.Environment["API_PASSWORD"] != "${API_PASSWORD:-}" {
		t.Fatal("API credential initialization must validate optional configured credentials without network access")
	}
	if api := services["api"]; api.Init == nil || *api.Init {
		t.Fatal("API must remain PID 1")
	}
	for _, name := range []string{"playground", "missions"} {
		if services[name].DependsOn["api"].Condition != "service_healthy" {
			t.Fatalf("%s must wait until its local API is ready", name)
		}
	}
	if services["playground"].DependsOn["missions"].Condition != "service_completed_successfully" {
		t.Fatal("Playground must wait until mission seeding finishes")
	}
	if missions := services["missions"]; !missions.ReadOnly || missions.Restart != "no" || missions.Image != services["ui"].Image {
		t.Fatal("Mission seeding must run once using the Admin image with a read-only filesystem")
	}
	for name, service := range services {
		if name == "api-credentials" || name == "api" || name == "playground" || name == "missions" {
			continue
		}
		for _, volume := range service.Volumes {
			if strings.HasPrefix(volume, "api-credentials:") {
				t.Fatalf("%s must not receive the API credential volume", name)
			}
		}
	}
	var compose struct{ Volumes map[string]any }
	readCIYAML(t, "../..", "compose.yaml", &compose)
	if _, exists := compose.Volumes["api-credentials"]; !exists {
		t.Fatal("Compose must declare the persistent API credential volume")
	}
	mcp := services["mcp"]
	if mcp.Init == nil || *mcp.Init || !slices.Equal(mcp.Command, []string{"--listen-address=:4273", "--allowed-hosts=localhost,127.0.0.1,::1,mcp"}) {
		t.Fatal("MCP must remain PID 1 with its configured listen address and allowed hosts")
	}
}
