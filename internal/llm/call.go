package llm

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	sqlcgen "github.com/Arthurobo/pennywise/internal/db/sqlc"
)

// CallLogger is the chokepoint for recording every LLM call. The DB-backed
// implementation lives in dbLogger below; tests can substitute a no-op or a
// recording fake.
type CallLogger interface {
	LogCall(ctx context.Context, e CallLogEntry) error
}

// CallLogEntry mirrors the llm_call_log table.
type CallLogEntry struct {
	Provider     string
	Model        string
	Purpose      Purpose
	LatencyMs    int64
	InputTokens  int
	OutputTokens int
	Success      bool
	ErrorMessage string
	At           time.Time
}

// Engine is the single chokepoint through which the rest of the codebase
// invokes LLMs. Every call goes through Complete, which:
//
//  1. Wraps ctx in a hard timeout.
//  2. Times the request.
//  3. Inserts a row into llm_call_log (success or failure).
//  4. Returns the raw response text.
//
// Callers feed the text into ParseResponse, which returns the unified
// expenses/query/unclear shape.
type Engine struct {
	Provider Provider
	Logger   CallLogger
	Timeout  time.Duration
}

// Complete sends the request through the provider with logging and timeout.
//
// If the wrapped provider call returns an error, Complete still returns that
// error to the caller — the log row is best-effort and never masks the real
// outcome.
func (e *Engine) Complete(ctx context.Context, purpose Purpose, req Request) (Response, error) {
	if e.Provider == nil {
		return Response{}, errors.New("llm engine: nil provider")
	}
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	resp, err := e.Provider.Complete(cctx, req)
	latency := time.Since(start)

	entry := CallLogEntry{
		Provider:  e.Provider.Name(),
		Model:     req.Model,
		Purpose:   purpose,
		LatencyMs: latency.Milliseconds(),
		Success:   err == nil,
		At:        time.Now().UTC(),
	}
	if err != nil {
		entry.ErrorMessage = truncErr(err.Error(), 500)
	} else {
		entry.InputTokens = resp.InputTokens
		entry.OutputTokens = resp.OutputTokens
	}
	if e.Logger != nil {
		if logErr := e.Logger.LogCall(ctx, entry); logErr != nil {
			slog.Warn("llm: log call failed", "error", logErr)
		}
	}
	return resp, err
}

// DBLogger persists CallLogEntry rows into llm_call_log via sqlc.
type DBLogger struct {
	Q *sqlcgen.Queries
}

func (l *DBLogger) LogCall(ctx context.Context, e CallLogEntry) error {
	in := sql.NullInt64{}
	if e.InputTokens > 0 {
		in = sql.NullInt64{Int64: int64(e.InputTokens), Valid: true}
	}
	out := sql.NullInt64{}
	if e.OutputTokens > 0 {
		out = sql.NullInt64{Int64: int64(e.OutputTokens), Valid: true}
	}
	errMsg := sql.NullString{}
	if e.ErrorMessage != "" {
		errMsg = sql.NullString{String: e.ErrorMessage, Valid: true}
	}
	success := int64(0)
	if e.Success {
		success = 1
	}
	return l.Q.InsertLLMCallLog(ctx, sqlcgen.InsertLLMCallLogParams{
		Provider:     e.Provider,
		Model:        e.Model,
		Purpose:      string(e.Purpose),
		LatencyMs:    e.LatencyMs,
		InputTokens:  in,
		OutputTokens: out,
		Success:      success,
		ErrorMessage: errMsg,
		CreatedAt:    e.At.Unix(),
	})
}

func truncErr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
