package safety

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		raw          string
		allowPrivate bool
		resolveDNS   bool
		wantErr      bool
		errContains  string
	}{
		// --- Схемы ---
		{"https ok", "https://example.com/path?q=1", false, false, false, ""},
		{"http ok", "http://example.com", false, false, false, ""},
		{"uppercase scheme normalized", "HTTPS://example.com", false, false, false, ""},
		{"ftp rejected", "ftp://example.com", false, false, true, "unsupported scheme"},
		{"file rejected", "file:///etc/passwd", false, false, true, "unsupported scheme"},
		{"javascript rejected", "javascript:alert(1)", false, false, true, "unsupported scheme"},
		{"empty scheme rejected", "//example.com", false, false, true, "unsupported scheme"},

		// --- Порт ---
		{"no port ok", "https://example.com", false, false, false, ""},
		{"port 443 ok", "https://example.com:443/", false, false, false, ""},
		{"port 80 ok", "http://example.com:80/", false, false, false, ""},
		{"port 8080 rejected", "http://example.com:8080/", false, false, true, "port 8080 not allowed"},
		{"port 22 rejected", "https://example.com:22/", false, false, true, "port 22 not allowed"},

		// --- IP-литералы: loopback ---
		{"127.0.0.1 rejected", "http://127.0.0.1/", false, false, true, "not allowed"},
		{"127.1.2.3 rejected", "http://127.1.2.3/", false, false, true, "not allowed"},
		{"::1 rejected", "http://[::1]/", false, false, true, "not allowed"},

		// --- IP-литералы: private ---
		{"10.0.0.1 rejected", "http://10.0.0.1/", false, false, true, "not allowed"},
		{"172.16.0.1 rejected", "http://172.16.0.1/", false, false, true, "not allowed"},
		{"172.31.255.255 rejected", "http://172.31.255.255/", false, false, true, "not allowed"},
		{"192.168.1.1 rejected", "http://192.168.1.1/", false, false, true, "not allowed"},
		{"fc00::1 rejected", "http://[fc00::1]/", false, false, true, "not allowed"},

		// --- IP-литералы: link-local / metadata ---
		{"169.254.169.254 rejected", "http://169.254.169.254/latest/meta-data/", false, false, true, "not allowed"},
		{"fe80::1 rejected", "http://[fe80::1]/", false, false, true, "not allowed"},

		// --- IP-литералы: CGNAT / reserved / multicast ---
		{"100.64.0.1 rejected", "http://100.64.0.1/", false, false, true, "not allowed"},
		{"0.0.0.0 rejected", "http://0.0.0.0/", false, false, true, "not allowed"},
		{"240.0.0.1 rejected", "http://240.0.0.1/", false, false, true, "not allowed"},
		{"224.0.0.1 rejected", "http://224.0.0.1/", false, false, true, "not allowed"},
		{"ff02::1 rejected", "http://[ff02::1]/", false, false, true, "not allowed"},

		// --- Граничные значения CIDR ---
		{"172.15.255.255 ok", "http://172.15.255.255/", false, false, false, ""},
		{"172.32.0.0 ok", "http://172.32.0.0/", false, false, false, ""},
		{"10.255.255.255 rejected", "http://10.255.255.255/", false, false, true, "not allowed"},
		{"192.167.1.1 ok", "http://192.167.1.1/", false, false, false, ""},
		{"192.169.1.1 ok", "http://192.169.1.1/", false, false, false, ""},

		// --- localhost-имена (без DNS) ---
		{"localhost rejected", "http://localhost/", false, false, true, "not allowed"},
		{"sub.localhost rejected", "http://foo.localhost/", false, false, true, "not allowed"},
		{"LOCALHOST case-insensitive", "http://LOCALHOST/", false, false, true, "not allowed"},

		// --- allowPrivate ---
		{"allowPrivate lets 127.0.0.1", "http://127.0.0.1/", true, false, false, ""},
		{"allowPrivate lets 10.0.0.1", "http://10.0.0.1/", true, false, false, ""},
		{"allowPrivate lets localhost", "http://localhost/", true, false, false, ""},

		// --- User-info / fragment ---
		{"user-info stripped", "https://user:pass@example.com/", false, false, false, ""},
		{"fragment stripped", "https://example.com/page#section", false, false, false, ""},

		// --- Длина / мусор ---
		{"empty", "", false, false, true, "empty url"},
		{"whitespace only", "   ", false, false, true, "empty url"},
		{"too long", "https://example.com/" + strings.Repeat("a", 2000), false, false, true, "exceeds"},
		{"no host", "https:///path", false, false, true, "empty host"},
		{"malformed", "http://[::1", false, false, true, "parse url"},

		// --- resolveDNS=false не делает LookupIP ---
		{"dns name not resolved when resolveDNS=false", "http://internal.corp/", false, false, false, ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ValidateURL(tc.raw, tc.allowPrivate, tc.resolveDNS)
			if tc.wantErr {
				require.Error(t, err)
				if tc.errContains != "" {
					assert.Contains(t, err.Error(), tc.errContains)
				}
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, got)
		})
	}
}

// TestValidateURL_UserInfoStripped — отдельно, потому что хотим проверить
// не только отсутствие ошибки, но и что креды реально вырезаны из результата.
func TestValidateURL_UserInfoStripped(t *testing.T) {
	t.Parallel()
	got, err := ValidateURL("https://user:pass@example.com/p?q=1#frag", false, false)
	require.NoError(t, err)
	assert.NotContains(t, got, "user")
	assert.NotContains(t, got, "pass")
	assert.NotContains(t, got, "#frag")
	assert.Contains(t, got, "example.com")
	assert.Contains(t, got, "q=1")
}

// FuzzValidateURL — ловит паники на неожиданном вводе.
// Запуск: go test -fuzz=FuzzValidateURL -fuzztime=30s ./internal/safety/
func FuzzValidateURL(f *testing.F) {
	seeds := []string{
		"https://example.com",
		"http://127.0.0.1",
		"http://[::1]",
		"http://169.254.169.254",
		"https://user:pass@example.com/x",
		"",
		"://",
		"http://例え.テスト/",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		// Главное — не паниковать. Ошибка или URL — оба валидны.
		_, _ = ValidateURL(raw, false, false)
	})
}
