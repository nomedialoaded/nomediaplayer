package subsonic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPingWebPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("<!DOCTYPE html><html></html>"))
	}))
	defer srv.Close()
	err := New(srv.URL, "u", "p").Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "web page") {
		t.Fatalf("expected a clear web page error, got %v", err)
	}
}
