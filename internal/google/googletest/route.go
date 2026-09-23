package googletest

import "net/http"

// route serves the data APIs beyond discovery (GA4 reports, Search Console
// performance, YouTube). It reports whether it answered.
func (s *Server) route(w http.ResponseWriter, r *http.Request, p string) bool {
	return false
}
