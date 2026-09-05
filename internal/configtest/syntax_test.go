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

func TestObservabilityPluginsAreWiredIntoEveryRuntimeMode(t *testing.T) {
	mainSource, err := os.ReadFile(filepath.Join("..", "..", "config", "mosdns.yaml.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	mainConfig := string(mainSource)
	for _, required := range []string{
		"exec: guarddns_metrics_collector main",
		"exec: guarddns_metrics_collector secure",
		"type: guarddns_tcp_server",
		"exec: guarddns_decision classified_domestic",
		"exec: guarddns_decision classified_overseas",
		"exec: guarddns_decision unknown",
	} {
		if !strings.Contains(mainConfig, required) {
			t.Errorf("mosdns.yaml.tmpl is missing %q", required)
		}
	}
	if strings.Contains(mainConfig, "exec: metrics_collector ") {
		t.Error("the upstream collector still counts expected client cancellations as errors")
	}

	for name, decisions := range map[string][]string{
		"foreign-secure.yaml": {
			"guarddns_decision secure_non_cn",
			"guarddns_decision secure_foreign",
		},
		"foreign-mihomo.yaml.tmpl": {
			"guarddns_decision fakeip_attempt",
			"guarddns_decision fakeip_answer",
			"guarddns_decision fallback_reuse_real",
			"guarddns_decision fallback_secure_lookup",
		},
	} {
		source, err := os.ReadFile(filepath.Join("..", "..", "config", name))
		if err != nil {
			t.Fatal(err)
		}
		config := string(source)
		for _, decision := range decisions {
			if !strings.Contains(config, decision) {
				t.Errorf("%s is missing %q", name, decision)
			}
		}
	}
}

func TestThreeLogicalDomainMappings(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "config", "mosdns.yaml.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	config := string(source)
	for _, required := range []string{
		"tag: real_ip_domains",
		"/data/real-ip.txt",
		"tag: overseas_domains",
		"/data/overseas.txt",
		"/usr/share/guarddns/rules/proxy.txt",
		"tag: domestic_domains",
		"/data/domestic.txt",
		"/usr/share/guarddns/rules/direct.txt",
		"exec: guarddns_decision real_ip",
		"exec: guarddns_decision overseas",
		"exec: guarddns_decision domestic",
	} {
		if !strings.Contains(config, required) {
			t.Errorf("mosdns.yaml.tmpl is missing logical mapping %q", required)
		}
	}
	for _, obsolete := range []string{
		"tag: force_secure_domains",
		"tag: force_fakeip_domains",
		"tag: force_direct_domains",
		"tag: global_domains",
		"tag: cn_domains",
		"tag: secure_domains",
	} {
		if strings.Contains(config, obsolete) {
			t.Errorf("obsolete standalone mapping remains: %q", obsolete)
		}
	}

	main := config[strings.Index(config, "tag: main_sequence"):]
	realIP := strings.Index(main, "qname $real_ip_domains")
	overseas := strings.Index(main, "qname $overseas_domains")
	domestic := strings.Index(main, "qname $domestic_domains")
	unknown := strings.Index(main, "guarddns_decision unknown")
	if realIP < 0 || overseas < 0 || domestic < 0 || unknown < 0 {
		t.Fatal("main sequence is missing a logical mapping")
	}
	if !(realIP < domestic && domestic < overseas && overseas < unknown) {
		t.Error("logical mapping priority is not real-IP -> domestic -> overseas -> unknown")
	}
}

func TestOnlyThreeSemanticDefaultDomainLists(t *testing.T) {
	defaultsPath := filepath.Join("..", "..", "config", "defaults")
	entries, err := os.ReadDir(defaultsPath)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]bool{
		"real-ip.txt":  true,
		"overseas.txt": true,
		"domestic.txt": true,
	}
	if len(entries) != len(expected) {
		t.Fatalf("default domain list count = %d, want %d", len(entries), len(expected))
	}
	for _, entry := range entries {
		if entry.IsDir() || !expected[entry.Name()] {
			t.Errorf("unexpected default domain list %q", entry.Name())
			continue
		}
		content, err := os.ReadFile(filepath.Join(defaultsPath, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(content), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "#") {
				t.Errorf("%s contains a comment instead of only domain rules", entry.Name())
			}
		}
	}
}

func TestSteamDownloadDomainsUseDomesticPath(t *testing.T) {
	path := filepath.Join("..", "..", "config", "defaults", "real-ip.txt")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"domain:steamcontent.com",
		"domain:steamserver.net",
		"domain:steamusercontent.com",
		"full:client-download.steampowered.com",
		"domain:steam-content-dnld-1.apac-1-cdn.cqloud.com",
		"full:steampipe.akamaized.net",
	} {
		if !strings.Contains(string(content), required) {
			t.Errorf("domestic.txt is missing Steam direct rule %q", required)
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
		"tag: classify_path",
		"resp_ip $cn_ips",
		"exec: goto validated_foreign_path",
		"# The three mappings are fast paths, not the final classifier.",
	} {
		if !strings.Contains(config, required) {
			t.Errorf("mosdns.yaml.tmpl is missing %q", required)
		}
	}

	// The recursive resolver decides CN membership; it does not answer for it.
	// Its reply is plaintext and poisoned for exactly the names that fall
	// through, so the classifier must discard it and re-resolve before the
	// client can be handed anything.
	classifier := config[strings.Index(config, "tag: classify_path"):]
	discard := strings.Index(classifier, "exec: drop_resp")
	reresolve := strings.Index(classifier, "exec: $forward_unbound")
	handoff := strings.Index(classifier, "exec: goto validated_foreign_path")
	if discard < 0 || reresolve < 0 || handoff < 0 {
		t.Fatal("classify_path no longer discards and re-resolves the classification answer")
	}
	if !(discard < reresolve && reresolve < handoff) {
		t.Error("classify_path can hand a client the plaintext classification answer")
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
