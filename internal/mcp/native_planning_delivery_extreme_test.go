//go:build !governed

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativePlanningLargeRPCIDRefusedBeforeParentRead(t *testing.T) {
	for _, id := range []any{strings.Repeat("x", 129), json.Number(strings.Repeat("1", 129)), true} {
		srv := NewExtremeServer(nil, "marshal", "")
		calls := 0
		srv.proposalCall = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			calls++
			return json.RawMessage(`{"context":"complete data"}`), nil
		}
		req := jsonRPCRequest{ID: id, Method: "tools/call", Params: json.RawMessage(`{"name":"marshal_project_contract_context","arguments":{}}`)}
		response := srv.handleStdioRequest(t.Context(), req)
		if response.Error == nil || calls != 0 {
			t.Fatal("oversized/invalid response identity reached complete parent reader")
		}
	}
}

type nativePlanningHTTPFailedWriter struct{ header http.Header }

func (w *nativePlanningHTTPFailedWriter) Header() http.Header       { return w.header }
func (w *nativePlanningHTTPFailedWriter) WriteHeader(int)           {}
func (w *nativePlanningHTTPFailedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestNativePlanningHTTPFailedOrCancelledDeliveryInvalidatesOnlyRead(t *testing.T) {
	for _, scenario := range []string{"failed-write", "cancel-before-write"} {
		t.Run(scenario, func(t *testing.T) {
			srv := NewExtremeServer(nil, "marshal", "")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			aborted := false
			srv.proposalCall = func(_ context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
				switch name {
				case "marshal_project_contract_context":
					if scenario == "cancel-before-write" {
						cancel()
					}
					return json.RawMessage(`{"context":"complete data","read_id":"http-read"}`), nil
				case "marshal_project_contract_context_abort":
					if string(args) != `{"read_id":"http-read"}` {
						t.Fatal("wrong read invalidated")
					}
					aborted = true
					return json.RawMessage(`{}`), nil
				default:
					t.Fatalf("unexpected tool %s", name)
					return nil, errors.New("unexpected tool")
				}
			}
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"marshal_project_contract_context","arguments":{}}}`)).WithContext(ctx)
			var w http.ResponseWriter = &nativePlanningHTTPFailedWriter{header: http.Header{}}
			if scenario == "cancel-before-write" {
				w = httptest.NewRecorder()
			}
			srv.Handler().ServeHTTP(w, r)
			if !aborted {
				t.Fatal("failed HTTP delivery retained read proof")
			}
		})
	}
}

type nativePlanningFailedWriter struct{}

func (nativePlanningFailedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestNativePlanningFailedOrCancelledWriterInvalidatesOnlyRead(t *testing.T) {
	for _, scenario := range []string{"failed-write", "cancel-before-write"} {
		t.Run(scenario, func(t *testing.T) {
			srv := NewExtremeServer(nil, "marshal", "")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			aborted := false
			srv.proposalCall = func(_ context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
				switch name {
				case "marshal_project_contract_context":
					if scenario == "cancel-before-write" {
						cancel()
					}
					return json.RawMessage(`{"context":"complete data","read_id":"read-once"}`), nil
				case "marshal_project_contract_context_abort":
					if string(args) != `{"read_id":"read-once"}` {
						t.Fatal("abort lost exact data selector")
					}
					aborted = true
					return json.RawMessage(`{}`), nil
				default:
					t.Fatal("unexpected authority method")
					return nil, errors.New("unexpected")
				}
			}
			input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"marshal_project_contract_context","arguments":{}}}` + "\n")
			var writer io.Writer = nativePlanningFailedWriter{}
			if scenario == "cancel-before-write" {
				writer = &bytes.Buffer{}
			}
			err := srv.ServeStdio(ctx, input, writer)
			if err == nil || !aborted {
				t.Fatal("failed/cancelled full tool response retained read proof", err)
			}
		})
	}
}
