package logging

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func setupTestLogger(t *testing.T) (*logrus.Logger, string) {
	t.Helper()
	tmpDir := t.TempDir()
	logger, cleanup := SetupLogging("", "", "info", tmpDir, false, nil)
	t.Cleanup(cleanup)
	return logger, tmpDir
}
func setupTestLoggerWithParams(t *testing.T, serverURL, apiKey, logLevel string) (*logrus.Logger, string) {
	t.Helper()
	tmpDir := t.TempDir()
	logger, cleanup := SetupLogging(serverURL, apiKey, logLevel, tmpDir, true, nil)
	t.Cleanup(cleanup)
	return logger, tmpDir
}

func TestSimpleFormatterOutputsMessageAndNewlineOnly(t *testing.T) {
	f := &SimpleFormatter{}
	testTime := time.Date(2025, 1, 1, 12, 34, 56, 123000000, time.UTC)
	entry := &logrus.Entry{
		Message: "hello world",
		Level:   logrus.InfoLevel,
		Time:    testTime,
		Data:    logrus.Fields{"key": "value"},
	}

	out, err := f.Format(entry)
	if err != nil {
		t.Fatalf("Format returned error: %v", err)
	}

	expected := "[12:34:56.123] hello world\n"
	if string(out) != expected {
		t.Errorf("expected %q, got %q", expected, string(out))
	}
}

func TestSimpleFormatterExcludesLevel(t *testing.T) {
	f := &SimpleFormatter{}
	entry := &logrus.Entry{
		Message: "test message",
		Level:   logrus.ErrorLevel,
		Time:    time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
	}

	out, err := f.Format(entry)
	if err != nil {
		t.Fatalf("Format returned error: %v", err)
	}

	result := string(out)
	if strings.Contains(result, "error") || strings.Contains(result, "ERROR") {
		t.Error("output should not contain log level")
	}
}

func TestSeqHookFireSendsCorrectJSON(t *testing.T) {
	var receivedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	hook := NewSeqHook(server.URL, "test-api-key")
	entry := &logrus.Entry{
		Message: "test log message",
		Level:   logrus.InfoLevel,
		Time:    time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC),
		Data:    logrus.Fields{"component": "test"},
		Logger:  logrus.New(),
	}

	err := hook.Fire(entry)
	if err != nil {
		t.Fatalf("Fire returned error: %v", err)
	}

	var event map[string]interface{}
	if err := json.Unmarshal(receivedBody, &event); err != nil {
		t.Fatalf("failed to unmarshal received body: %v", err)
	}

	if event["@mt"] != "test log message" {
		t.Errorf("expected @mt=%q, got %q", "test log message", event["@mt"])
	}
	if event["@l"] != "info" {
		t.Errorf("expected @l=%q, got %q", "info", event["@l"])
	}
	if _, ok := event["@t"]; !ok {
		t.Error("expected @t timestamp field to be present")
	}
	if event["component"] != "test" {
		t.Errorf("expected component=%q, got %q", "test", event["component"])
	}
}

func TestSeqHookSendsCorrectHeaders(t *testing.T) {
	var receivedHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	hook := NewSeqHook(server.URL, "my-secret-key")
	entry := &logrus.Entry{
		Message: "header test",
		Level:   logrus.WarnLevel,
		Time:    time.Now(),
		Data:    logrus.Fields{},
		Logger:  logrus.New(),
	}

	err := hook.Fire(entry)
	if err != nil {
		t.Fatalf("Fire returned error: %v", err)
	}

	if ct := receivedHeaders.Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type=%q, got %q", "application/json", ct)
	}
	if ak := receivedHeaders.Get("X-Seq-ApiKey"); ak != "my-secret-key" {
		t.Errorf("expected X-Seq-ApiKey=%q, got %q", "my-secret-key", ak)
	}
}

func TestSeqHookOmitsApiKeyHeaderWhenEmpty(t *testing.T) {
	var receivedHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	hook := NewSeqHook(server.URL, "")
	entry := &logrus.Entry{
		Message: "no key test",
		Level:   logrus.InfoLevel,
		Time:    time.Now(),
		Data:    logrus.Fields{},
		Logger:  logrus.New(),
	}

	err := hook.Fire(entry)
	if err != nil {
		t.Fatalf("Fire returned error: %v", err)
	}

	if ak := receivedHeaders.Get("X-Seq-ApiKey"); ak != "" {
		t.Errorf("expected no X-Seq-ApiKey header, got %q", ak)
	}
}

func TestSeqHookPostsToCorrectEndpoint(t *testing.T) {
	var receivedPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	hook := NewSeqHook(server.URL, "")
	entry := &logrus.Entry{
		Message: "path test",
		Level:   logrus.InfoLevel,
		Time:    time.Now(),
		Data:    logrus.Fields{},
		Logger:  logrus.New(),
	}

	if err := hook.Fire(entry); err != nil {
		t.Fatalf("Fire returned error: %v", err)
	}

	if receivedPath != "/api/events/raw" {
		t.Errorf("expected path=%q, got %q", "/api/events/raw", receivedPath)
	}
}

func TestSeqHookLevelsReturnsAllLevels(t *testing.T) {
	hook := NewSeqHook("http://localhost", "")
	levels := hook.Levels()

	if len(levels) != len(logrus.AllLevels) {
		t.Errorf("expected %d levels, got %d", len(logrus.AllLevels), len(levels))
	}
}

func TestSetupLoggingReturnsNonNilLogger(t *testing.T) {
	logger, _ := setupTestLogger(t)

	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestSetupLoggingParsesLogLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected logrus.Level
	}{
		{"debug", logrus.DebugLevel},
		{"info", logrus.InfoLevel},
		{"warn", logrus.WarnLevel},
		{"error", logrus.ErrorLevel},
		{"invalid", logrus.InfoLevel},
		{"", logrus.InfoLevel},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			logger, _ := setupTestLoggerWithParams(t, "", "", tc.input)
			if logger.GetLevel() != tc.expected {
				t.Errorf("for input %q: expected level %v, got %v", tc.input, tc.expected, logger.GetLevel())
			}
		})
	}
}

func TestSetupLoggingCreatesLogFile(t *testing.T) {
	_, tmpDir := setupTestLogger(t)

	logsDir := filepath.Join(tmpDir, "logs")
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		t.Fatalf("failed to read logs dir: %v", err)
	}

	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "reforge_") && strings.HasSuffix(e.Name(), ".log") {
			found = true
			break
		}
	}

	if !found {
		t.Error("expected log file with pattern reforge_*.log to be created in logs/")
	}
}

func TestSetupLoggingSetsSimpleFormatter(t *testing.T) {
	logger, _ := setupTestLogger(t)

	if _, ok := logger.Formatter.(*SimpleFormatter); !ok {
		t.Errorf("expected SimpleFormatter, got %T", logger.Formatter)
	}
}

func TestSetupLoggingAddsSeqHookWhenServerURLProvided(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger, _ := setupTestLoggerWithParams(t, server.URL, "key", "info")

	hooks := logger.Hooks
	found := false
	for _, levelHooks := range hooks {
		for _, h := range levelHooks {
			if _, ok := h.(*SeqHook); ok {
				found = true
				break
			}
		}
	}

	if !found {
		t.Error("expected SeqHook to be added when serverURL is provided")
	}
}

func TestSetupLoggingNoSeqHookWhenServerURLEmpty(t *testing.T) {
	logger, _ := setupTestLogger(t)

	hooks := logger.Hooks
	for _, levelHooks := range hooks {
		for _, h := range levelHooks {
			if _, ok := h.(*SeqHook); ok {
				t.Error("expected no SeqHook when serverURL is empty")
				return
			}
		}
	}
}

func TestSetupLoggingNoSeqHookWhenSeqDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	logger, cleanup := SetupLogging(server.URL, "key", "info", tmpDir, false, nil)
	t.Cleanup(cleanup)

	for _, levelHooks := range logger.Hooks {
		for _, h := range levelHooks {
			if _, ok := h.(*SeqHook); ok {
				t.Error("expected no SeqHook when seq_enabled is false")
				return
			}
		}
	}
}

func TestNewSeqHookTrimsTrailingSlash(t *testing.T) {
	hook := NewSeqHook("http://localhost:5341/", "key")
	if hook.serverURL != "http://localhost:5341" {
		t.Errorf("expected trailing slash to be trimmed, got %q", hook.serverURL)
	}
}

func TestSeqHookCircuitBreakerDisablesAfterMaxFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.Close()

	hook := NewSeqHook(server.URL, "")

	entry := &logrus.Entry{
		Message: "fail",
		Level:   logrus.InfoLevel,
		Time:    time.Now(),
		Data:    logrus.Fields{},
		Logger:  logrus.New(),
	}

	for i := 0; i < seqMaxFailures; i++ {
		if err := hook.Fire(entry); err != nil {
			t.Fatalf("Fire should not return error, got: %v", err)
		}
		if i < seqMaxFailures-1 && hook.disabled {
			t.Fatalf("hook disabled too early at iteration %d", i)
		}
	}

	if !hook.disabled {
		t.Error("expected hook to be disabled after seqMaxFailures consecutive failures")
	}
	if hook.failures < seqMaxFailures {
		t.Errorf("expected failures >= %d, got %d", seqMaxFailures, hook.failures)
	}
}

func TestSeqHookCircuitBreakerSkipsWhenDisabled(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	hook := NewSeqHook(server.URL, "")
	hook.disabled = true // pre-disable

	entry := &logrus.Entry{
		Message: "should not reach server",
		Level:   logrus.InfoLevel,
		Time:    time.Now(),
		Data:    logrus.Fields{},
		Logger:  logrus.New(),
	}

	if err := hook.Fire(entry); err != nil {
		t.Fatalf("Fire returned error: %v", err)
	}

	if callCount != 0 {
		t.Errorf("expected 0 HTTP calls when disabled, got %d", callCount)
	}
}

func TestSeqHookCircuitBreakerResetsOnSuccess(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	dead.Close()

	hook := NewSeqHook(dead.URL, "")

	failEntry := &logrus.Entry{
		Message: "fail",
		Level:   logrus.InfoLevel,
		Time:    time.Now(),
		Data:    logrus.Fields{},
		Logger:  logrus.New(),
	}

	for i := 0; i < seqMaxFailures-1; i++ {
		_ = hook.Fire(failEntry)
	}
	if hook.failures != seqMaxFailures-1 {
		t.Fatalf("expected %d failures, got %d", seqMaxFailures-1, hook.failures)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	hook.serverURL = server.URL

	successEntry := &logrus.Entry{
		Message: "success",
		Level:   logrus.InfoLevel,
		Time:    time.Now(),
		Data:    logrus.Fields{},
		Logger:  logrus.New(),
	}

	if err := hook.Fire(successEntry); err != nil {
		t.Fatalf("Fire returned error: %v", err)
	}

	if hook.failures != 0 {
		t.Errorf("expected failures reset to 0 after success, got %d", hook.failures)
	}
	if hook.disabled {
		t.Error("hook should not be disabled after a successful send")
	}
}

func TestSetupLoggingWritesToLogFile(t *testing.T) {
	logger, tmpDir := setupTestLoggerWithParams(t, "", "", "info")

	logger.Info("test log entry")

	logsDir := filepath.Join(tmpDir, "logs")
	entries, _ := os.ReadDir(logsDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "reforge_") && strings.HasSuffix(e.Name(), ".log") {
			content, err := os.ReadFile(filepath.Join(logsDir, e.Name()))
			if err != nil {
				t.Fatalf("failed to read log file: %v", err)
			}
			if !strings.Contains(string(content), "test log entry") {
				t.Error("expected log file to contain 'test log entry'")
			}
			return
		}
	}
	t.Error("log file not found")
}
