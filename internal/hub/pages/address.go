package pages

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// clientAddress returns the address of the client: the connection's, or,
// when the connection comes from one of trusted, the rightmost address of
// X-Forwarded-For that is not, since each proxy appends the address it was
// reached from and only the trusted ones can be believed.
func clientAddress(r *http.Request, trusted []netip.Prefix) string {
	conn, ok := peer(r)
	if !ok {
		return Peer(r)
	}
	if !isTrusted(conn, trusted) {
		return conn.String()
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	client := conn
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			// What a trusted proxy did not write cannot be believed.
			return client.String()
		}
		client = a.Unmap()
		if !isTrusted(client, trusted) {
			break
		}
	}
	return client.String()
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Peer is the address of r's connection, without its port: a proxy's when
// the client came through one.
func Peer(r *http.Request) string {
	if a, ok := peer(r); ok {
		return a.String()
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// peer is the address of r's connection, false when it is no IP address.
func peer(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, err := netip.ParseAddr(host)
	return a.Unmap(), err == nil
}
