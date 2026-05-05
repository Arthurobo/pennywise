package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// FakeTelegram is an httptest.Server that mimics the Telegram Bot API for
// the methods the bot uses. Every captured call is appended to Calls so
// tests can assert outbound sendMessage / editMessageText / getFile /
// answerCallbackQuery payloads.
//
// Constructor wires it as the BaseURL of a *telegram.Client; the client's
// callJSON path then hits the fake instead of api.telegram.org.
type FakeTelegram struct {
	Server *httptest.Server

	mu    sync.Mutex
	Calls []FakeCall

	// Files maps file_id → bytes for getFile/download. Tests register
	// expected attachments via WithFile() before triggering the bot.
	files map[string]fakeFile

	// nextMessageID auto-increments so each sendMessage gets a unique ID.
	nextMessageID int64
}

type FakeCall struct {
	Method string         // bot API method, e.g. "sendMessage"
	Body   map[string]any // decoded JSON body
}

type fakeFile struct {
	mime string
	body []byte
}

// NewFakeTelegram starts the httptest server. Always call Close in
// t.Cleanup (already wired here).
func NewFakeTelegram(t *testing.T) *FakeTelegram {
	t.Helper()
	f := &FakeTelegram{
		files:         map[string]fakeFile{},
		nextMessageID: 1000,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", f.serve)
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Server.Close)
	return f
}

func (f *FakeTelegram) serve(w http.ResponseWriter, r *http.Request) {
	// Path shape:
	//   /bot<token>/<method>          → JSON API call
	//   /file/bot<token>/<file_path>  → file download
	if strings.HasPrefix(r.URL.Path, "/file/") {
		f.serveFileDownload(w, r)
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	if len(parts) != 2 {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	method := parts[1]
	body := map[string]any{}
	_ = json.NewDecoder(r.Body).Decode(&body)

	f.mu.Lock()
	f.Calls = append(f.Calls, FakeCall{Method: method, Body: body})
	f.mu.Unlock()

	switch method {
	case "sendMessage":
		f.respondSendMessage(w)
	case "editMessageText", "deleteMessage", "answerCallbackQuery",
		"setMyCommands", "setMyDescription", "setMyShortDescription",
		"pinChatMessage", "unpinAllChatMessages":
		f.respondOK(w)
	case "getMe":
		f.respondGetMe(w)
	case "getFile":
		f.respondGetFile(w, body)
	case "getUpdates":
		// Tests inject Updates directly via Dispatcher.Dispatch — they
		// don't poll. Always return an empty list.
		f.respondGetUpdates(w)
	default:
		f.respondOK(w)
	}
}

func (f *FakeTelegram) respondOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true})
}

func (f *FakeTelegram) respondSendMessage(w http.ResponseWriter) {
	f.mu.Lock()
	f.nextMessageID++
	id := f.nextMessageID
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": true,
		"result": map[string]any{
			"message_id": id,
			"chat":       map[string]any{"id": int64(0), "type": "private"},
			"date":       0,
		},
	})
}

func (f *FakeTelegram) respondGetMe(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": true,
		"result": map[string]any{
			"id":       int64(123),
			"is_bot":   true,
			"username": "test_bot",
		},
	})
}

func (f *FakeTelegram) respondGetUpdates(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []any{}})
}

func (f *FakeTelegram) respondGetFile(w http.ResponseWriter, body map[string]any) {
	fileID, _ := body["file_id"].(string)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": true,
		"result": map[string]any{
			"file_id":   fileID,
			"file_path": fmt.Sprintf("test/%s.bin", url.PathEscape(fileID)),
		},
	})
}

func (f *FakeTelegram) serveFileDownload(w http.ResponseWriter, r *http.Request) {
	// /file/bot<token>/<file_path>
	idx := strings.Index(r.URL.Path[len("/file/"):], "/")
	if idx < 0 {
		http.Error(w, "bad file path", http.StatusBadRequest)
		return
	}
	filePath := r.URL.Path[len("/file/")+idx+1:]
	// Recover the file_id from the encoded filename `test/<id>.bin`.
	fileID := strings.TrimSuffix(strings.TrimPrefix(filePath, "test/"), ".bin")
	if id, err := url.PathUnescape(fileID); err == nil {
		fileID = id
	}
	f.mu.Lock()
	file, ok := f.files[fileID]
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", file.mime)
	_, _ = w.Write(file.body)
}

// WithFile registers binary content for a given file_id. The dispatcher's
// getFile + downloadFile sequence will return these bytes when the test
// pushes a Photo or Document Update with this file_id.
func (f *FakeTelegram) WithFile(fileID, mime string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[fileID] = fakeFile{mime: mime, body: body}
}

// Captured returns a snapshot of all calls so far, in order.
func (f *FakeTelegram) Captured() []FakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]FakeCall, len(f.Calls))
	copy(out, f.Calls)
	return out
}

// LastSendMessage returns the most recent sendMessage body, or nil.
func (f *FakeTelegram) LastSendMessage() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.Calls) - 1; i >= 0; i-- {
		if f.Calls[i].Method == "sendMessage" {
			return f.Calls[i].Body
		}
	}
	return nil
}

// LastEditMessage returns the most recent editMessageText body, or nil.
func (f *FakeTelegram) LastEditMessage() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.Calls) - 1; i >= 0; i-- {
		if f.Calls[i].Method == "editMessageText" {
			return f.Calls[i].Body
		}
	}
	return nil
}

// CountCalls returns how many calls landed for the given method name.
func (f *FakeTelegram) CountCalls(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.Calls {
		if c.Method == method {
			n++
		}
	}
	return n
}
