package httpapi

import (
	"net/http"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Mesteriis/veda-wiki/internal/mcp"
	"github.com/Mesteriis/veda-wiki/internal/mcp/client"
)

// remoteMCPBodyLimit bounds one MCP request; reading tools take small inputs.
const remoteMCPBodyLimit = 1 << 20

// remoteMCPSlots bounds concurrent remote MCP requests: each one buffers a REST
// answer in memory, so many parallel calls must queue instead of piling up.
var remoteMCPSlots = make(chan struct{}, 8)

// remoteMCP serves read-only MCP over Streamable HTTP to an agent that holds a
// source-scoped access key. The owner key and the reader token are refused:
// writing stays with the owner's local editor. Each request builds the reader
// tool set over an in-process client that calls this handler with the same
// key, so the key's scope applies exactly as it does for REST.
func (h handler) remoteMCP(rw http.ResponseWriter, r *http.Request) {
	if h.Public || h.Store == nil {
		http.NotFound(rw, r)
		return
	}
	key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || key == "" || h.authorized(r) || h.readerAuthorized(r) {
		unauthorized(rw)
		return
	}
	_, valid, err := h.Store.auth().ScopedView(key, time.Now())
	if err != nil {
		http.Error(rw, "Access unavailable", http.StatusServiceUnavailable)
		return
	}
	if !valid {
		unauthorized(rw)
		return
	}
	select {
	case remoteMCPSlots <- struct{}{}:
		defer func() { <-remoteMCPSlots }()
	case <-r.Context().Done():
		return
	}
	c, err := client.NewInProcess(h, key, r.Host, r.RemoteAddr)
	if err != nil {
		unauthorized(rw)
		return
	}
	server := mcp.NewServer(c)
	sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server },
		// hostAllowed has already checked Host against the configured names;
		// the SDK's own loopback check would refuse a reverse proxy that
		// reaches this listener on loopback with the public name.
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: remoteMCPBodyLimit, DisableLocalhostProtection: true}).ServeHTTP(rw, r)
}

func unauthorized(rw http.ResponseWriter) {
	rw.Header().Set("WWW-Authenticate", `Bearer realm="veda"`)
	jsonResponse(rw, http.StatusUnauthorized, map[string]string{"error": "access key required"})
}
