package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadAdditionalAdministrators(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_BRIDGE_CONFIG_DIR", dir)
	t.Setenv("APP_ENV", "dev")
	t.Setenv("HUB_USERNAME", "")
	if err := os.WriteFile(filepath.Join(dir, "dev.yaml"), []byte("auth:\n  bootstrap_username: admin\n  admin_usernames: [operator, reviewer]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.BootstrapUsername != "admin" || !reflect.DeepEqual(cfg.Auth.AdminUsernames, []string{"operator", "reviewer"}) {
		t.Fatal("additional administrator YAML did not preserve both grants")
	}
	if len(Default().Auth.AdminUsernames) != 0 {
		t.Fatal("default config must not grant additional administrators")
	}
}

func TestApplyEnvStrictWorkspace(t *testing.T) {
	t.Setenv("BRIDGE_STRICT_WORKSPACE", "true")
	want := []string{"/custom/coq", "/custom/isabelle"}
	t.Setenv("BRIDGE_STRICT_WORKSPACE_READ_ONLY", filepath.Join(want[0])+string(filepath.ListSeparator)+filepath.Join(want[1]))
	cfg := Default()

	applyEnv(&cfg)

	if !cfg.Bridge.StrictWorkspace {
		t.Fatal("BRIDGE_STRICT_WORKSPACE did not enable strict mode")
	}
	if !reflect.DeepEqual(cfg.Bridge.StrictWorkspaceReadOnly, want) {
		t.Fatalf("strict read-only paths = %#v, want %#v", cfg.Bridge.StrictWorkspaceReadOnly, want)
	}
}
