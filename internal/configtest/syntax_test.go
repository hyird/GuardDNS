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

func TestNonStandardPortsFollowRequestHierarchy(t *testing.T) {
	files := map[string][]string{
		"mosdns.yaml.tmpl": {
			"listen: '0.0.0.0:5304'",
			"addr: 127.0.0.1:5305",
			"addr: 127.0.0.1:5306",
			"http: '0.0.0.0:5308'",
		},
		"unbound.conf.tmpl": {
			"interface: 127.0.0.1@5306",
			"forward-addr: 127.0.0.1@5307",
		},
		"unbound-recursive.conf.tmpl": {
			"interface: 127.0.0.1@5305",
		},
	}
	for name, required := range files {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join("..", "..", "config", name))
			if err != nil {
				t.Fatal(err)
			}
			config := string(source)
			for _, fragment := range required {
				if !strings.Contains(config, fragment) {
					t.Errorf("%s breaks the request-ordered 5304-5308 port layout: missing %q", name, fragment)
				}
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
				"addr: 127.0.0.1:5306",
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
		"forward-addr: 127.0.0.1@5307",
	} {
		if !strings.Contains(config, required) {
			t.Errorf("unbound.conf.tmpl is missing %q", required)
		}
	}
}

func TestUnknownDomainsUseRealIPClassification(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "config", "mosdns.yaml.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	config := string(source)
	for _, required := range []string{
		"tag: cn_path",
		"resp_ip $cn_ips",
		"exec: goto validated_foreign_path",
		"# Lists are fast paths, not the final classifier.",
	} {
		if !strings.Contains(config, required) {
			t.Errorf("mosdns.yaml.tmpl is missing %q", required)
		}
	}

	// The recursive resolver decides CN membership; it does not answer for it.
	// Its reply is plaintext and poisoned for exactly the names that fall
	// through, so the classifier must discard it and re-resolve before the
	// client can be handed anything.
	classifier := config[strings.Index(config, "tag: cn_path"):]
	discard := strings.Index(classifier, "exec: drop_resp")
	reresolve := strings.Index(classifier, "exec: $forward_unbound")
	handoff := strings.Index(classifier, "exec: goto validated_foreign_path")
	if discard < 0 || reresolve < 0 || handoff < 0 {
		t.Fatal("cn_path no longer discards and re-resolves the classification answer")
	}
	if !(discard < reresolve && reresolve < handoff) {
		t.Error("cn_path can hand a client the plaintext classification answer")
	}
	if !strings.Contains(config, "addr: 127.0.0.1:5305") {
		t.Error("mosdns.yaml.tmpl does not reach the recursive CN resolver")
	}
	if strings.Contains(config, "# Unknown names fail closed to encrypted DNS.") {
		t.Fatal("unknown names still bypass real-IP CN classification")
	}
	if strings.Contains(config, "tag: cn_cache") {
		t.Fatal("CN classification cache can retain a final fake-IP response")
	}

	for _, name := range []string{"foreign-secure.yaml", "foreign-mihomo.yaml.tmpl"} {
		source, err := os.ReadFile(filepath.Join("..", "..", "config", name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(source), "tag: validated_foreign_path") {
			t.Errorf("%s does not implement the validated NON-CN entry", name)
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
