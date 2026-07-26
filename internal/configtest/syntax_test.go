package configtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IrineSistiana/mosdns/v5/coremain"
	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

func TestConfigTemplatesDecode(t *testing.T) {
	files := []string{
		"mosdns.yaml.tmpl",
		"foreign-secure.yaml",
		"foreign-mihomo.yaml.tmpl",
	}
	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join("..", "..", "config", name))
			if err != nil {
				t.Fatal(err)
			}
			rendered := strings.NewReplacer(
				"__LOG_LEVEL__", "warn",
				"__AUTO_FORWARD_ENDPOINT__", "127.0.0.1:5353",
			).Replace(string(source))
			if strings.Contains(rendered, "__") {
				t.Fatalf("%s contains an unresolved placeholder", name)
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(rendered), 0600); err != nil {
				t.Fatal(err)
			}

			cfg := decodeConfig(t, path)
			if len(cfg.Plugins) == 0 {
				t.Fatalf("%s decoded without plugins", name)
			}
		})
	}
}

func TestSecurePathsUseValidatingUnboundOverTLS(t *testing.T) {
	for _, name := range []string{"foreign-secure.yaml", "foreign-mihomo.yaml.tmpl"} {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join("..", "..", "config", name))
			if err != nil {
				t.Fatal(err)
			}
			config := string(source)
			for _, required := range []string{
				"tag: secure_unbound",
				"addr: 127.0.0.1:5335",
			} {
				if !strings.Contains(config, required) {
					t.Errorf("%s is missing %q", name, required)
				}
			}
			for _, forbidden := range []string{
				"addr: https://",
				"dial_addr:",
				"bootstrap:",
			} {
				if strings.Contains(config, forbidden) {
					t.Errorf("%s bypasses validating Unbound with %q", name, forbidden)
				}
			}
		})
	}

	source, err := os.ReadFile(filepath.Join("..", "..", "config", "unbound.conf.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	config := string(source)
	for _, required := range []string{
		`module-config: "validator iterator"`,
		`auto-trust-anchor-file: "/run/guarddns/unbound/root.key"`,
		"do-not-query-localhost: no",
		"forward-addr: 127.0.0.1@5336",
	} {
		if !strings.Contains(config, required) {
			t.Errorf("unbound.conf.tmpl is missing %q", required)
		}
	}
}

func decodeConfig(t *testing.T, path string) *coremain.Config {
	t.Helper()
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	cfg := new(coremain.Config)
	if err := v.Unmarshal(cfg, func(config *mapstructure.DecoderConfig) {
		config.ErrorUnused = true
		config.TagName = "yaml"
		config.WeaklyTypedInput = true
	}); err != nil {
		t.Fatal(err)
	}
	return cfg
}
