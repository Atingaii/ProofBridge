package hub

import "net/http"

// ServeHTTP serves the same authenticated routes and middleware as Run without
// starting its listener, signal handling, recovery, or model catalog lifecycle.
// It lets callers host the existing Hub handler in an isolated HTTP server.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.httpSrv.Handler.ServeHTTP(w, r)
}
