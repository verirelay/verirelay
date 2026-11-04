package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/netutil"
	"golang.org/x/sys/unix"
)

// maxConnections caps the client connections open at once, whatever the open
// file limit, because each one holds memory.
const maxConnections = 4096

// ListenAndServe serves handler on addr. It accepts at most connectionLimit
// client connections at once; further clients wait to be accepted.
func ListenAndServe(addr string, handler http.Handler) error {
	var files unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &files); err != nil {
		return fmt.Errorf("read the open file limit: %w", err)
	}
	listener, err := new(net.ListenConfig).Listen(context.Background(), "tcp", addr)
	if err != nil {
		return err
	}
	limit := connectionLimit(files.Cur)
	slog.Info("VeriRelay listening", "component", "server", "addr", addr, "max_connections", limit)
	srv := &http.Server{
		Handler:           closeUnreadBody(handler),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	return srv.Serve(netutil.LimitListener(listener, limit))
}

// connectionLimit returns maxConnections, or a quarter of the open file limit
// if that is lower: a forward holds a second descriptor for its Upstream, and
// the stores and the AMD KDS need their own.
func connectionLimit(openFiles uint64) int {
	return int(min(maxConnections, openFiles/4))
}

// closeUnreadBody answers a request whose body the handler did not read to
// the end with Connection: close, and reads no more of that body. net/http
// would otherwise read up to 256 KiB of the rest, with no deadline, before
// releasing the connection, so a client trickling a body could hold it.
func closeUnreadBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Connection", "close")
		body := &trackedBody{ReadCloser: r.Body, header: w.Header()}
		r.Body = body
		next.ServeHTTP(w, r)
		if !body.eof {
			_ = http.NewResponseController(w).SetReadDeadline(time.Now())
		}
	})
}

// trackedBody is a request body that drops the Connection: close of its
// response once it has been read to the end.
type trackedBody struct {
	io.ReadCloser
	header http.Header
	eof    bool
}

func (b *trackedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if errors.Is(err, io.EOF) && !b.eof {
		b.eof = true
		b.header.Del("Connection")
	}
	return n, err
}
