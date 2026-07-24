// Package logging configures logrus with file output, a simple formatter, and
// an optional Seq structured-log hook.
package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// SimpleFormatter outputs log messages prefixed with timestamp: [15:04:05.000] message
type SimpleFormatter struct{}

// Format renders a log entry with a timestamp header followed by the message.
func (f *SimpleFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	timestamp := entry.Time.Format("15:04:05.000")
	return []byte(fmt.Sprintf("[%s] %s\n", timestamp, entry.Message)), nil
}

// SeqHook sends logs to Seq
type SeqHook struct {
	serverURL string
	apiKey    string
	client    *http.Client
	mu        sync.Mutex
	failures  int
	disabled  bool
}

const seqMaxFailures = 5

// NewSeqHook creates a new Seq hook
func NewSeqHook(serverURL, apiKey string) *SeqHook {
	return &SeqHook{
		serverURL: strings.TrimSuffix(serverURL, "/"),
		apiKey:    apiKey,
		client:    &http.Client{Timeout: 5 * time.Second},
	}
}

// Levels returns the log levels this hook should fire on
func (hook *SeqHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

// Fire sends the log entry to Seq. After seqMaxFailures consecutive delivery
// failures the hook disables itself and logs a single warning to stderr so that
// a misconfigured Seq URL does not degrade encoding performance.
func (hook *SeqHook) Fire(entry *logrus.Entry) error {
	hook.mu.Lock()
	if hook.disabled {
		hook.mu.Unlock()
		return nil
	}
	hook.mu.Unlock()

	// Build Seq event
	event := map[string]interface{}{
		"@t":  entry.Time.Format(time.RFC3339Nano),
		"@mt": entry.Message,
		"@l":  entry.Level.String(),
	}

	// Add fields
	for k, v := range entry.Data {
		event[k] = v
	}

	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	// Send to Seq
	req, err := http.NewRequest("POST", hook.serverURL+"/api/events/raw", bytes.NewBuffer(data))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	if hook.apiKey != "" {
		req.Header.Set("X-Seq-ApiKey", hook.apiKey)
	}

	resp, err := hook.client.Do(req)
	if err != nil {
		hook.mu.Lock()
		hook.failures++
		if hook.failures >= seqMaxFailures {
			hook.disabled = true
			hook.mu.Unlock()
			fmt.Fprintf(os.Stderr, "WARNING: Seq logging disabled after %d consecutive failures (last error: %v)\n", seqMaxFailures, err)
		} else {
			hook.mu.Unlock()
		}
		return nil // Silently ignore Seq connection failures
	}
	defer resp.Body.Close()

	// Reset failure counter on success.
	hook.mu.Lock()
	hook.failures = 0
	hook.mu.Unlock()

	return nil
}

// bufferWriter is an io.Writer that buffers all writes in memory.
// Call Flush to replay the buffered content into a destination writer.
type bufferWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *bufferWriter) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// Flush copies all buffered bytes to dst. Safe to call once.
func (b *bufferWriter) Flush(dst io.Writer) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, err := io.Copy(dst, &b.buf)
	return err
}

// SetupLogging creates and configures a logrus.Logger with file output,
// SimpleFormatter, optional Seq hook, and the specified log level.
//
// consoleWriter, if non-nil, receives log lines in addition to the log file.
// Pass os.Stdout for plain output or a tui.UI.Writer() for TUI-integrated output.
// When nil, output goes to os.Stdout.
//
// It returns the logger and a cleanup function that closes the log file.
func SetupLogging(serverURL, apiKey, logLevel, execDir string, seqEnabled bool, consoleWriter io.Writer) (*logrus.Logger, func()) {
	logger := logrus.New()
	cleanup := func() {} // no-op default

	if consoleWriter == nil {
		consoleWriter = os.Stdout
	}

	// Create log file with timestamp inside a dedicated logs/ subdirectory.
	logsDir := filepath.Join(execDir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create logs directory: %v\n", err)
	}
	logFileName := fmt.Sprintf("reforge_%s.log", time.Now().Format("2006-01-02_15-04-05"))
	logFilePath := filepath.Join(logsDir, logFileName)
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open log file: %v\n", err)
		logger.SetOutput(consoleWriter)
	} else {
		mw := io.MultiWriter(consoleWriter, logFile)
		logger.SetOutput(mw)
		fmt.Fprintf(consoleWriter, "Logging to: %s\n", logFilePath)
		cleanup = func() {
			logger.SetOutput(io.Discard)
			logFile.Close()
		}
	}
	// Custom formatter that only outputs the message (no timestamp/level prefix)
	logger.SetFormatter(&SimpleFormatter{})
	level, err := logrus.ParseLevel(logLevel)
	if err != nil {
		level = logrus.InfoLevel
	}
	logger.SetLevel(level)
	if serverURL != "" && seqEnabled {
		hook := NewSeqHook(serverURL, apiKey)
		logger.AddHook(hook)
		logger.Debug("Seq logging hook enabled")
	}

	return logger, cleanup
}

// SetupEarlyLogging creates a logger that buffers all output in memory until
// the TUI is ready. Call the returned flush function once with the real writer
// (e.g. tui.UI.Writer()) to open the log file and replay buffered content.
//
// This avoids the "double log file" problem that arises when SetupLogging is
// called twice: once before the TUI starts (plain stdout) and once after.
func SetupEarlyLogging(logLevel string, execDir ...string) (*logrus.Logger, func(serverURL, apiKey, execDir string, seqEnabled bool, consoleWriter io.Writer) (*logrus.Logger, func())) {
	buf := &bufferWriter{}
	early := logrus.New()

	var earlyLogFile *os.File
	dir := ""
	if len(execDir) > 0 {
		dir = execDir[0]
	}
	if dir != "" {
		logsDir := filepath.Join(dir, "logs")
		if err := os.MkdirAll(logsDir, 0755); err == nil {
			logFileName := fmt.Sprintf("reforge_%s.log", time.Now().Format("2006-01-02_15-04-05"))
			logFilePath := filepath.Join(logsDir, logFileName)
			if f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
				earlyLogFile = f
			}
		}
	}

	var writers []io.Writer
	writers = append(writers, os.Stderr, buf)
	if earlyLogFile != nil {
		writers = append(writers, earlyLogFile)
	}

	early.SetOutput(io.MultiWriter(writers...))
	early.SetFormatter(&SimpleFormatter{})
	level, err := logrus.ParseLevel(logLevel)
	if err != nil {
		level = logrus.InfoLevel
	}
	early.SetLevel(level)

	flush := func(serverURL, apiKey, execDir string, seqEnabled bool, consoleWriter io.Writer) (*logrus.Logger, func()) {
		early.SetOutput(io.Discard)
		if earlyLogFile != nil {
			earlyLogFile.Close()
		}

		real, cleanup := SetupLogging(serverURL, apiKey, logLevel, execDir, seqEnabled, consoleWriter)

		if mw, ok := real.Out.(io.Writer); ok {
			if err := buf.Flush(mw); err != nil {
				real.Warnf("Failed to flush early log buffer: %v", err)
			}
		}

		return real, cleanup
	}

	return early, flush
}
