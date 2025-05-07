package httpmiddleware

import (
	"net/http"

	"github.com/Dal-Papa/simple-torrent/common"
)

func Liveness(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// liveness response
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte("OK"))
			common.HandleError(err)
			return
		}
		h.ServeHTTP(w, r)
	})
}
