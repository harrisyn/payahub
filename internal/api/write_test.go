package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A handler that waits longer than the server's write timeout still gets its
// answer out once it extends the deadline; without it the client sees none.
func TestExtendWriteOutlastsTheServerTimeout(t *testing.T) {
	for _, extend := range []bool{false, true} {
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if extend {
				extendWrite(w, time.Second)
			}
			time.Sleep(300 * time.Millisecond)
			io.WriteString(w, "answer")
		}))
		srv.Config.WriteTimeout = 100 * time.Millisecond
		srv.Start()
		res, err := http.Get(srv.URL)
		got := ""
		if err == nil {
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			got = string(b)
		}
		srv.Close()
		if extend != (got == "answer") {
			t.Fatalf("extend=%v: got %q, err %v", extend, got, err)
		}
	}
}
