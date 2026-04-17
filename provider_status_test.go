package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeProviderBaseURLUsesProviderDefaults(t *testing.T) {
	cases := []struct {
		provider string
		expected string
	}{
		{provider: "anthropic", expected: "https://api.anthropic.com"},
		{provider: "openai", expected: "https://api.openai.com"},
		{provider: "openai-compatible", expected: "https://api.openai.com"},
		{provider: "openrouter", expected: "https://openrouter.ai/api/v1"},
		{provider: "ollama", expected: "http://localhost:11434"},
	}

	for _, tc := range cases {
		if got := normalizeProviderBaseURL(tc.provider, ""); got != tc.expected {
			t.Fatalf("normalizeProviderBaseURL(%q) = %q, want %q", tc.provider, got, tc.expected)
		}
	}
}

func TestFetchOpenRouterKeyStatusParsesCurrentKeyPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/key" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"label":"prod-key","limit":100,"limit_remaining":74.5,"usage":25.5,"usage_daily":5.5,"usage_monthly":20.25,"byok_usage":2.25,"include_byok_in_limit":true,"is_free_tier":false,"is_management_key":true,"limit_reset":"monthly","expires_at":"2027-12-31T23:59:59Z"}}`))
	}))
	defer server.Close()

	status, normalized, err := FetchOpenRouterKeyStatus(context.Background(), server.URL, "test-key")
	if err != nil {
		t.Fatalf("FetchOpenRouterKeyStatus: %v", err)
	}
	if normalized != server.URL {
		t.Fatalf("expected normalized base URL %q, got %q", server.URL, normalized)
	}
	if status.Label != "prod-key" {
		t.Fatalf("expected label to be parsed, got %#v", status)
	}
	if status.Limit == nil || *status.Limit != 100 {
		t.Fatalf("expected limit to be parsed, got %#v", status.Limit)
	}
	if status.LimitRemaining == nil || *status.LimitRemaining != 74.5 {
		t.Fatalf("expected limit_remaining to be parsed, got %#v", status.LimitRemaining)
	}
	if status.UsageMonthly == nil || *status.UsageMonthly != 20.25 {
		t.Fatalf("expected usage_monthly to be parsed, got %#v", status.UsageMonthly)
	}
	if status.BYOKUsage == nil || *status.BYOKUsage != 2.25 {
		t.Fatalf("expected byok_usage to be parsed, got %#v", status.BYOKUsage)
	}
	if !status.IsManagementKey {
		t.Fatalf("expected management key flag to be parsed")
	}
}

func TestFetchOpenRouterCreditsParsesCreditsPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/credits" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"total_credits":100.5,"total_usage":25.75}}`))
	}))
	defer server.Close()

	credits, normalized, err := FetchOpenRouterCredits(context.Background(), server.URL, "test-key")
	if err != nil {
		t.Fatalf("FetchOpenRouterCredits: %v", err)
	}
	if normalized != server.URL {
		t.Fatalf("expected normalized base URL %q, got %q", server.URL, normalized)
	}
	if credits.TotalCredits == nil || *credits.TotalCredits != 100.5 {
		t.Fatalf("expected total_credits to be parsed, got %#v", credits.TotalCredits)
	}
	if credits.TotalUsage == nil || *credits.TotalUsage != 25.75 {
		t.Fatalf("expected total_usage to be parsed, got %#v", credits.TotalUsage)
	}
}

func TestRuntimeStateShowProviderStatusShowsOpenRouterBudgetDetails(t *testing.T) {
	originalKeyFetcher := fetchOpenRouterKeyStatus
	originalCreditsFetcher := fetchOpenRouterCredits
	t.Cleanup(func() {
		fetchOpenRouterKeyStatus = originalKeyFetcher
		fetchOpenRouterCredits = originalCreditsFetcher
	})

	fetchOpenRouterKeyStatus = func(ctx context.Context, baseURL, apiKey string) (OpenRouterKeyStatus, string, error) {
		if baseURL != "https://openrouter.ai/api/v1" {
			t.Fatalf("expected normalized OpenRouter base URL, got %q", baseURL)
		}
		if apiKey != "test-key" {
			t.Fatalf("expected configured API key, got %q", apiKey)
		}
		return OpenRouterKeyStatus{
			Label:           "prod-key",
			Limit:           providerStatusFloat64Ptr(100),
			LimitRemaining:  providerStatusFloat64Ptr(74.5),
			Usage:           providerStatusFloat64Ptr(25.5),
			UsageDaily:      providerStatusFloat64Ptr(5.5),
			UsageMonthly:    providerStatusFloat64Ptr(20.25),
			IsManagementKey: true,
			LimitReset:      "monthly",
		}, baseURL, nil
	}
	fetchOpenRouterCredits = func(ctx context.Context, baseURL, apiKey string) (OpenRouterCredits, string, error) {
		return OpenRouterCredits{
			TotalCredits: providerStatusFloat64Ptr(100.5),
			TotalUsage:   providerStatusFloat64Ptr(25.75),
		}, baseURL, nil
	}

	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{color: false},
		cfg: Config{
			Provider: "openrouter",
			Model:    "openai/gpt-5.4",
			APIKey:   "test-key",
		},
		session: &Session{
			Provider: "openrouter",
			Model:    "openai/gpt-5.4",
		},
	}

	if err := rt.showProviderStatus(); err != nil {
		t.Fatalf("showProviderStatus: %v", err)
	}

	rendered := out.String()
	for _, needle := range []string{
		"https://openrouter.ai/api/v1",
		"api_key",
		"configured",
		"key_type",
		"management",
		"limit_remaining",
		"74.50",
		"account_balance",
		"74.75",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected provider status to contain %q, got %q", needle, rendered)
		}
	}
	if strings.Contains(rendered, "http://localhost:11434") {
		t.Fatalf("provider status should not fall back to the Ollama base URL, got %q", rendered)
	}
}

func TestRuntimeStateShowProviderStatusDescribesOpenAIBudgetLimits(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{color: false},
		cfg: Config{
			Provider: "openai",
			Model:    "gpt-5.4",
			APIKey:   "test-key",
		},
		session: &Session{
			Provider: "openai",
			Model:    "gpt-5.4",
		},
	}

	if err := rt.showProviderStatus(); err != nil {
		t.Fatalf("showProviderStatus: %v", err)
	}

	rendered := out.String()
	for _, needle := range []string{
		"https://api.openai.com",
		"No documented exact prepaid-balance API endpoint is available.",
		openAIUsageAPIDocsURL,
		openAIUsageDashboardHelpURL,
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected OpenAI provider status to contain %q, got %q", needle, rendered)
		}
	}
}

func TestRuntimeStateShowProviderStatusDescribesAnthropicBudgetLimits(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{color: false},
		cfg: Config{
			Provider: "anthropic",
			Model:    "claude-sonnet-4-6",
			APIKey:   "test-key",
		},
		session: &Session{
			Provider: "anthropic",
			Model:    "claude-sonnet-4-6",
		},
	}

	if err := rt.showProviderStatus(); err != nil {
		t.Fatalf("showProviderStatus: %v", err)
	}

	rendered := out.String()
	for _, needle := range []string{
		"https://api.anthropic.com",
		"Billing page shows the organization's available credit balance.",
		"Organization admin API key required.",
		anthropicUsageCostDocsURL,
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected Anthropic provider status to contain %q, got %q", needle, rendered)
		}
	}
}

func providerStatusFloat64Ptr(value float64) *float64 {
	return &value
}
