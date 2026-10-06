package web

import (
	"net/url"
	"strconv"
)

// Only an explicit exact 127.0.0.1 HTTP origin can widen the development proxy
// boundary. No wildcard, remote host, credentials, path or fragment is accepted.
func validDevelopmentOrigin(origin string) bool {
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	port, e := strconv.Atoi(u.Port())
	return e == nil && port >= 1 && port <= 65535 && origin == "http://127.0.0.1:"+strconv.Itoa(port)
}
