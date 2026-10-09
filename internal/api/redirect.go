package api

import (
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"strings"
	"time"
)

// Redirected is a redirect a client refused to follow, because it left the
// origin the request was sent to.
type Redirected struct {
	From, To string // scheme://host of each end
}

func (r *Redirected) Error() string {
	return fmt.Sprintf("refused a redirect from %s to %s", r.From, r.To)
}

// maxRedirects is as many as net/http follows by default.
const maxRedirects = 10

// sameOrigin is the redirect policy of every client celadon builds: a
// redirect is followed only within the scheme, host and port the request
// was sent to. net/http drops the Authorization header when a redirect
// leaves the host, but not the X-Aether-Key header a profile can send its
// key in, nor the body a 307 or 308 sends again — a vault passphrase, a
// secret being stored — and it drops nothing on a redirect from https down
// to plain http on the same host.
func sameOrigin(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	if from := via[0].URL; origin(from) != origin(req.URL) {
		return &Redirected{From: from.Scheme + "://" + from.Host, To: req.URL.Scheme + "://" + req.URL.Host}
	}
	return nil
}

// origin is what a redirect may not change, the default port spelled out.
func origin(u *neturl.URL) string {
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	if port == "" {
		switch scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

// httpClient is the client a service is reached through: a copy of the
// given one — a test's — or one over the transport the service's own client
// would have built, bounding the wait for an answer's headers. Either way
// sameOrigin is its redirect policy; a client that brings that policy has to
// bring its transport too.
func httpClient(given *http.Client, headerTimeout time.Duration) *http.Client {
	var hc http.Client
	if given != nil {
		hc = *given
	} else {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.ResponseHeaderTimeout = headerTimeout
		hc.Transport = t
	}
	hc.CheckRedirect = sameOrigin
	return &hc
}
