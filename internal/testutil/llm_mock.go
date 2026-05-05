package testutil

import (
	"context"
	"sync"

	"github.com/Arthurobo/pennywise/internal/llm"
)

// MockProvider implements llm.Provider with canned responses for tests.
// All Complete calls are recorded in Calls (mutex-guarded) so tests can
// assert what the dispatcher actually sent — system prompt content,
// images attached, model selected, etc.
//
// Inject via DispatcherOpts.LLMProvider or Handler.LLMProvider.
type MockProvider struct {
	NameStr string
	Resp    llm.Response
	Err     error

	mu    sync.Mutex
	Calls []llm.Request
}

func (m *MockProvider) Name() string {
	if m.NameStr == "" {
		return "mock"
	}
	return m.NameStr
}

func (m *MockProvider) Complete(_ context.Context, r llm.Request) (llm.Response, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, r)
	m.mu.Unlock()
	return m.Resp, m.Err
}

func (m *MockProvider) Test(_ context.Context, _ string) error { return m.Err }

// LastCall returns the most recently recorded Request, or zero value if none.
func (m *MockProvider) LastCall() llm.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.Calls) == 0 {
		return llm.Request{}
	}
	return m.Calls[len(m.Calls)-1]
}

// CallCount returns the number of Complete invocations.
func (m *MockProvider) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Calls)
}

// MockResponseJSON wraps a raw JSON body in an llm.Response. Convenience
// for the most common assertion shape — canned parse output.
func MockResponseJSON(body string) llm.Response {
	return llm.Response{Text: body, InputTokens: 100, OutputTokens: 50}
}
