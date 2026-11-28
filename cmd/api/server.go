package main

import (
	"net/http"
	"time"
)

// serverTimeouts bound every phase of an HTTP request.
type serverTimeouts struct {
	ReadHeader, Read, Write, Idle time.Duration
}

// defaultServerTimeouts: Read and Write leave room for a 500 KiB photo upload on a slow phone
// connection. They do not limit WebSockets: net/http clears the connection deadlines when a handler
// hijacks it, and the realtime handler bounds its own reads and writes.
var defaultServerTimeouts = serverTimeouts{
	ReadHeader: 5 * time.Second,
	Read:       30 * time.Second,
	Write:      30 * time.Second,
	Idle:       2 * time.Minute,
}

// newHTTPServer builds the HTTP server. Without Read and Write timeouts a client that sends its
// body or reads the response slowly would hold a goroutine, a socket and a buffer indefinitely.
func newHTTPServer(addr string, handler http.Handler, t serverTimeouts) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: t.ReadHeader,
		ReadTimeout:       t.Read,
		WriteTimeout:      t.Write,
		IdleTimeout:       t.Idle,
	}
}
